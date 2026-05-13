package storage

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Minimal S3 mock: supports single PUT, initiate/uploadPart/complete/abort.
// Does not verify SigV4 signatures (those have their own crypto tests);
// asserts the *protocol shape* so refactors in s3.go can't accidentally
// break ordering or omit a step.

type mockS3 struct {
	mu sync.Mutex

	singleObjects   map[string][]byte // key -> full body
	parts           map[string][][]byte
	completed       map[string]bool
	aborted         map[string]bool
	multipartCalls  int
	completePartIDs map[string][]int // last list of part numbers in CompleteMultipartUpload
}

func newMockS3() *mockS3 {
	return &mockS3{
		singleObjects:   map[string][]byte{},
		parts:           map[string][][]byte{},
		completed:       map[string]bool{},
		aborted:         map[string]bool{},
		completePartIDs: map[string][]int{},
	}
}

func (m *mockS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	objKey := strings.TrimLeft(r.URL.Path, "/")
	// Strip bucket prefix in path-style URLs (we run with ForcePathStyle).
	if i := strings.Index(objKey, "/"); i > 0 {
		objKey = objKey[i+1:]
	}

	switch {
	case r.Method == "POST" && q.Has("uploads"):
		// initiate multipart
		m.mu.Lock()
		m.multipartCalls++
		uploadID := fmt.Sprintf("u-%d", m.multipartCalls)
		m.parts[uploadID] = nil
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w,
			`<?xml version="1.0"?><InitiateMultipartUploadResult><UploadId>%s</UploadId></InitiateMultipartUploadResult>`,
			uploadID)

	case r.Method == "PUT" && q.Has("partNumber"):
		uploadID := q.Get("uploadId")
		body, _ := io.ReadAll(r.Body)
		etag := fmt.Sprintf("\"%x\"", md5.Sum(body))
		m.mu.Lock()
		// Pad slice so index = partNumber - 1.
		var pn int
		fmt.Sscanf(q.Get("partNumber"), "%d", &pn)
		for len(m.parts[uploadID]) < pn {
			m.parts[uploadID] = append(m.parts[uploadID], nil)
		}
		m.parts[uploadID][pn-1] = body
		m.mu.Unlock()
		w.Header().Set("ETag", etag)
		w.WriteHeader(200)

	case r.Method == "POST" && q.Has("uploadId"):
		// complete multipart
		uploadID := q.Get("uploadId")
		var spec completeMultipartUpload
		body, _ := io.ReadAll(r.Body)
		if err := xml.Unmarshal(body, &spec); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		m.mu.Lock()
		ids := make([]int, len(spec.Parts))
		for i, p := range spec.Parts {
			ids[i] = p.PartNumber
		}
		m.completePartIDs[uploadID] = ids
		m.completed[uploadID] = true
		// stitch parts → singleObjects
		var blob []byte
		for _, p := range m.parts[uploadID] {
			blob = append(blob, p...)
		}
		m.singleObjects[objKey] = blob
		m.mu.Unlock()
		fmt.Fprintf(w, `<?xml version="1.0"?><CompleteMultipartUploadResult/>`)

	case r.Method == "DELETE" && q.Has("uploadId"):
		m.mu.Lock()
		m.aborted[q.Get("uploadId")] = true
		m.mu.Unlock()
		w.WriteHeader(204)

	case r.Method == "PUT":
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.singleObjects[objKey] = body
		m.mu.Unlock()
		w.WriteHeader(200)

	default:
		http.Error(w, "unhandled mock route: "+r.Method+" "+r.URL.String(), 400)
	}
}

func newUploader(t *testing.T) (*S3Uploader, *mockS3, func()) {
	mock := newMockS3()
	srv := httptest.NewServer(mock)
	u := &S3Uploader{
		cfg: S3Config{
			Endpoint:       srv.URL,
			Region:         "us-east-1",
			Bucket:         "test-bucket",
			AccessKey:      "AKIAEXAMPLE",
			SecretKey:      "secret",
			ForcePathStyle: true,
		},
		client: srv.Client(),
	}
	return u, mock, srv.Close
}

func TestS3_SinglePut_RoundTrips(t *testing.T) {
	u, mock, cleanup := newUploader(t)
	defer cleanup()

	body := []byte("hello world")
	if err := u.Put(context.Background(), "snap.db",
		bytes.NewReader(body), "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	mock.mu.Lock()
	got := mock.singleObjects["snap.db"]
	mock.mu.Unlock()
	if !bytes.Equal(got, body) {
		t.Fatalf("single PUT body mismatch: got %d bytes, want %d", len(got), len(body))
	}
	if mock.multipartCalls != 0 {
		t.Fatal("small object incorrectly used multipart")
	}
}

func TestS3_PutLarge_SwitchesToMultipart(t *testing.T) {
	u, mock, cleanup := newUploader(t)
	defer cleanup()

	// 5 GiB synthetic stream — way over the 4.5 GB switchover. Use a
	// repeating-pattern reader so we don't allocate 5 GiB of RAM.
	size := int64(5 * 1024 * 1024 * 1024)
	body := newPatternReader(size)

	if err := u.PutLarge(context.Background(), "big.db", body, size,
		"application/octet-stream"); err != nil {
		t.Fatalf("PutLarge: %v", err)
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()

	if mock.multipartCalls != 1 {
		t.Fatalf("expected 1 multipart initiate, got %d", mock.multipartCalls)
	}
	var doneFor string
	for u, ok := range mock.completed {
		if ok {
			doneFor = u
		}
	}
	if doneFor == "" {
		t.Fatal("multipart never completed")
	}
	ids := mock.completePartIDs[doneFor]
	// 5 GiB / 64 MiB = exactly 80 parts.
	if len(ids) != 80 {
		t.Fatalf("expected 80 parts, got %d", len(ids))
	}
	// Parts must be in ascending order; S3 enforces this.
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("parts not strictly ascending: %v", ids)
		}
	}
	if len(mock.aborted) != 0 {
		t.Fatalf("clean run should not abort, got %v", mock.aborted)
	}
}

// Pattern reader = deterministic bytes without RAM cost. Caller knows the
// total size; we just emit bytes until they ReadFull what they need.
type patternReader struct {
	pos   int64
	limit int64
}

func newPatternReader(size int64) io.ReadSeeker {
	return &patternReader{limit: size}
}
func (p *patternReader) Read(b []byte) (int, error) {
	if p.pos >= p.limit {
		return 0, io.EOF
	}
	n := int64(len(b))
	if p.pos+n > p.limit {
		n = p.limit - p.pos
	}
	for i := int64(0); i < n; i++ {
		b[i] = byte((p.pos + i) & 0xff)
	}
	p.pos += n
	return int(n), nil
}
func (p *patternReader) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		p.pos = off
	case io.SeekCurrent:
		p.pos += off
	case io.SeekEnd:
		p.pos = p.limit + off
	}
	return p.pos, nil
}

// Verify the SigV4 signer doesn't crash on empty bodies (used by initiate +
// abort). Spot-check the Authorization header shape.
func TestS3_SigV4_HeaderShape(t *testing.T) {
	req, _ := http.NewRequest("PUT", "https://s3.example.com/b/k", nil)
	req.Header.Set("Host", "s3.example.com")
	sum := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" // sha256("")
	if err := signV4(req, sum, mustParseTime("2024-01-02T03:04:05Z"),
		"us-east-1", "s3", "AKEX", "secret"); err != nil {
		t.Fatal(err)
	}
	auth := req.Header.Get("Authorization")
	for _, want := range []string{
		"AWS4-HMAC-SHA256",
		"Credential=AKEX/20240102/us-east-1/s3/aws4_request",
		"SignedHeaders=",
		"Signature=",
	} {
		if !strings.Contains(auth, want) {
			t.Errorf("Authorization missing %q: %s", want, auth)
		}
	}
}

func TestS3_PutStreaming_RoundTrips(t *testing.T) {
	u, mock, cleanup := newUploader(t)
	defer cleanup()

	// 200 KB body — pipes through one streamed chunk (8 MiB chunk size).
	body := bytes.Repeat([]byte{0xAB}, 200*1024)
	r := bytes.NewReader(body)
	if err := u.PutStreaming(context.Background(), "stream.db", r,
		int64(len(body)), "application/octet-stream"); err != nil {
		t.Fatalf("PutStreaming: %v", err)
	}
	mock.mu.Lock()
	got := mock.singleObjects["stream.db"]
	mock.mu.Unlock()
	if len(got) == 0 {
		t.Fatal("body never reached server")
	}
	// Streaming endpoint sends aws-chunked frames; the mock S3 here echoes
	// them back verbatim (it doesn't understand the framing). We just
	// confirm bytes flowed and length is non-zero — the unit-of-truth for
	// "did SigV4 work" is real S3, hit via the validate.sh integration suite.
}

func TestS3_PutStreaming_MultiChunk(t *testing.T) {
	u, mock, cleanup := newUploader(t)
	defer cleanup()

	// 17 MiB → 3 chunks (two full 8 MiB + 1 MiB tail).
	body := bytes.Repeat([]byte{0xCD}, 17*1024*1024)
	if err := u.PutStreaming(context.Background(), "stream-multi.db",
		bytes.NewReader(body), int64(len(body)), ""); err != nil {
		t.Fatalf("PutStreaming: %v", err)
	}
	mock.mu.Lock()
	got := len(mock.singleObjects["stream-multi.db"])
	mock.mu.Unlock()
	if got == 0 {
		t.Fatal("multi-chunk streaming didn't hit the server")
	}
}

func mustParseTime(s string) time.Time {
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return v
}

// Unused but kept to silence linter — we want hex around when we extend the
// payload-hash assertions.
var _ = hex.EncodeToString
var _ atomic.Int32
