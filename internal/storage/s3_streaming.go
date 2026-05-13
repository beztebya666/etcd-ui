// Streaming SigV4 (aka "Streaming Signed Payload" /
// STREAMING-AWS4-HMAC-SHA256-PAYLOAD).
//
// Why: the regular single-PUT signer hashes the *entire* body up front
// before sending the first byte. For a 50 GiB etcd snapshot this:
//
//   - holds an extra SHA-256 pass over disk (slow on spinners);
//   - doubles I/O on hosts where the snapshot file is on NFS;
//   - blocks the goroutine for minutes before any network activity.
//
// Streaming SigV4 signs each chunk independently. The body is sent in the
// AWS framing:
//
//   <chunk-size-hex>;chunk-signature=<sig>\r\n
//   <chunk-bytes>\r\n
//   …
//   0;chunk-signature=<final-sig>\r\n\r\n
//
// We support it for the single-PUT path *and* for individual parts inside
// multipart (where the per-part hash budget is smaller but still wasteful
// at 64 MiB × 80 parts on a 5 GiB transfer).
//
// Caller switches on by calling PutStreaming(ctx, key, reader, size). The
// schedule code prefers PutLarge → which picks single vs multipart; this
// file exposes a third option for sources that have no seekable file (e.g.
// piping straight off /snapshot HTTP stream).

package storage

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	streamingPayloadType = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD"
	streamingChunkSize   = 8 * 1024 * 1024 // 8 MiB — keeps chunk header overhead < 0.001%
)

// PutStreaming uploads `body` of exact `size` bytes to S3 using chunked-
// payload SigV4. Body need not be seekable. Caller is responsible for not
// passing a stream that drains faster than the network can absorb.
//
// `size` MUST be exact; SigV4 requires Content-Length up front. Passing
// the wrong size produces SignatureDoesNotMatch from S3.
func (u *S3Uploader) PutStreaming(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	objKey := strings.TrimLeft(u.cfg.Prefix+key, "/")
	endpoint, err := url.Parse(u.cfg.Endpoint)
	if err != nil {
		return err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	uri := u.objectURL(endpoint, objKey, nil)

	now := time.Now().UTC()
	date := now.Format("20060102")
	credentialScope := date + "/" + u.cfg.Region + "/s3/aws4_request"

	encLen := streamingEncodedLen(size)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uri.String(), nil)
	if err != nil {
		return err
	}
	req.ContentLength = encLen
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Content-Encoding", "aws-chunked")
	req.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	req.Header.Set("X-Amz-Decoded-Content-Length", strconv.FormatInt(size, 10))
	req.Header.Set("X-Amz-Content-Sha256", streamingPayloadType)
	req.Header.Set("Host", uri.Host)

	// Sign the seed request (everything but the body).
	signedHeaders, canonicalHeaders := canonicalHeaders(req)
	canonicalReq := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		canonicalQuery(req.URL),
		canonicalHeaders,
		signedHeaders,
		streamingPayloadType,
	}, "\n")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		now.Format("20060102T150405Z"),
		credentialScope,
		hexHash(canonicalReq),
	}, "\n")
	signingKey := deriveSigningKey(u.cfg.SecretKey, date, u.cfg.Region, "s3")
	seedSig := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential="+u.cfg.AccessKey+"/"+credentialScope+
			", SignedHeaders="+signedHeaders+", Signature="+seedSig)

	// Wrap body in the streaming-chunked encoder. The encoder rotates the
	// signature chain so each chunk's signature depends on the previous
	// chunk's — same scheme S3 uses for streaming.
	req.Body = io.NopCloser(newStreamingChunkReader(
		body, signingKey, credentialScope,
		now.Format("20060102T150405Z"), seedSig,
	))

	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("s3 streaming put %s: %s — %s", objKey, resp.Status, string(msg))
	}
	return nil
}

// streamingChunkReader emits an aws-chunked stream while computing each
// chunk's SigV4 signature. Lazy: doesn't buffer the whole body, just one
// chunk at a time.
type streamingChunkReader struct {
	src         io.Reader
	signingKey  []byte
	credScope   string
	dateTime    string
	prevSig     string

	buf      *bytes.Buffer
	chunkBuf []byte
	done     bool
}

func newStreamingChunkReader(src io.Reader, signingKey []byte, credScope, dateTime, seedSig string) *streamingChunkReader {
	return &streamingChunkReader{
		src:        src,
		signingKey: signingKey,
		credScope:  credScope,
		dateTime:   dateTime,
		prevSig:    seedSig,
		buf:        &bytes.Buffer{},
		chunkBuf:   make([]byte, streamingChunkSize),
	}
}

func (s *streamingChunkReader) Read(p []byte) (int, error) {
	for s.buf.Len() == 0 && !s.done {
		if err := s.fillNextChunk(); err != nil {
			return 0, err
		}
	}
	if s.buf.Len() == 0 && s.done {
		return 0, io.EOF
	}
	return s.buf.Read(p)
}

func (s *streamingChunkReader) fillNextChunk() error {
	n, err := io.ReadFull(s.src, s.chunkBuf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return err
	}
	if n == 0 {
		// Emit final 0-length chunk and we're done.
		sig := s.signChunk(nil)
		fmt.Fprintf(s.buf, "0;chunk-signature=%s\r\n\r\n", sig)
		s.done = true
		s.prevSig = sig
		return nil
	}
	chunk := s.chunkBuf[:n]
	sig := s.signChunk(chunk)
	fmt.Fprintf(s.buf, "%x;chunk-signature=%s\r\n", n, sig)
	s.buf.Write(chunk)
	s.buf.WriteString("\r\n")
	s.prevSig = sig
	// If the underlying reader EOF'd alongside this last chunk, schedule
	// the trailing 0-length chunk on the next call.
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		// Use writeFinal lazily so the buffer flushes the data chunk first.
	}
	return nil
}

func (s *streamingChunkReader) signChunk(chunk []byte) string {
	emptyHash := hex.EncodeToString(sha256.New().Sum(nil))
	chunkHash := emptyHash
	if len(chunk) > 0 {
		sum := sha256.Sum256(chunk)
		chunkHash = hex.EncodeToString(sum[:])
	}
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256-PAYLOAD",
		s.dateTime,
		s.credScope,
		s.prevSig,
		emptyHash,
		chunkHash,
	}, "\n")
	return hex.EncodeToString(hmacSHA256(s.signingKey, []byte(stringToSign)))
}

// streamingEncodedLen returns the exact aws-chunked encoded length for a
// body of `size` bytes. Necessary because Go's HTTP transport refuses to
// PUT with a wrong Content-Length, and S3 streaming SigV4 requires
// Content-Length (it's the *encoded* length, not the raw body).
//
// Per-chunk wire format:
//   <hex-size>;chunk-signature=<64-hex>\r\n<data>\r\n
//
// Final chunk:
//   0;chunk-signature=<64-hex>\r\n\r\n
func streamingEncodedLen(size int64) int64 {
	const sigSuffix = ";chunk-signature="
	const hexSigLen = 64
	const crlf = 2
	final := int64(len("0")+len(sigSuffix)+hexSigLen+crlf+crlf)

	if size == 0 {
		return final
	}
	full := size / streamingChunkSize
	tail := size % streamingChunkSize
	hexFull := int64(len(strconv.FormatInt(streamingChunkSize, 16)))
	perFull := hexFull + int64(len(sigSuffix)) + hexSigLen + crlf + streamingChunkSize + crlf
	enc := full * perFull
	if tail > 0 {
		hexTail := int64(len(strconv.FormatInt(tail, 16)))
		enc += hexTail + int64(len(sigSuffix)) + hexSigLen + crlf + tail + crlf
	}
	return enc + final
}

// deriveSigningKey computes the SigV4 signing key chain. Kept separate so
// streaming + non-streaming paths share one implementation.
func deriveSigningKey(secret, date, region, service string) []byte {
	return hmacSHA256(hmacSHA256(hmacSHA256(hmacSHA256(
		[]byte("AWS4"+secret), []byte(date)), []byte(region)), []byte(service)), []byte("aws4_request"))
}

// PutLargeStreaming is the streaming counterpart of PutLarge. Use when the
// body is exact-sized but not seekable, e.g. when piping a snapshot reader
// straight from etcd's gRPC stream without spilling to disk.
//
// For < 4.5 GB we use a single chunked-payload PUT.
// For ≥ 4.5 GB we use multipart, but each part is itself uploaded with
// chunked-payload SigV4 so we never have to seek-then-hash a 64 MiB buffer
// in RAM. The hash chain runs against the streaming reader chunk-by-chunk.
func (u *S3Uploader) PutLargeStreaming(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	if size < multipartThreshold {
		return u.PutStreaming(ctx, key, body, size, contentType)
	}
	return u.putMultipartStreaming(ctx, key, body, size, contentType)
}

// putMultipartStreaming runs the multipart dance with chunked-payload signing
// per part. Layout:
//
//   POST   <key>?uploads                     → uploadId
//   PUT    <key>?partNumber=N&uploadId=…    (body framed as aws-chunked)
//   …
//   POST   <key>?uploadId=…                  with completion XML
//
// We still serialise reads from the source (one Read per part), but the
// part bytes go straight to the wire framed by streamingChunkReader. Memory
// usage: O(8 MiB chunk + small framing overhead), not 64 MiB.
func (u *S3Uploader) putMultipartStreaming(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	objKey := strings.TrimLeft(u.cfg.Prefix+key, "/")
	endpoint, err := url.Parse(u.cfg.Endpoint)
	if err != nil {
		return err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	uploadID, err := u.initiateMultipart(ctx, endpoint, objKey, contentType)
	if err != nil {
		return fmt.Errorf("initiate multipart-streaming: %w", err)
	}
	completed := false
	defer func() {
		if !completed {
			_ = u.abortMultipart(context.Background(), endpoint, objKey, uploadID)
		}
	}()

	var parts []completedPart
	br := bufio.NewReaderSize(body, partSize)
	for partNum := 1; ; partNum++ {
		remaining := size - int64(partNum-1)*int64(partSize)
		if remaining <= 0 {
			break
		}
		partSz := int64(partSize)
		if remaining < partSz {
			partSz = remaining
		}
		etag, err := u.uploadPartStreaming(ctx, endpoint, objKey, uploadID, partNum, io.LimitReader(br, partSz), partSz)
		if err != nil {
			return fmt.Errorf("upload streaming part %d: %w", partNum, err)
		}
		parts = append(parts, completedPart{ETag: etag, PartNumber: partNum})
	}

	if err := u.completeMultipart(ctx, endpoint, objKey, uploadID, parts); err != nil {
		return fmt.Errorf("complete streaming multipart: %w", err)
	}
	completed = true
	return nil
}

// uploadPartStreaming sends one part using chunked-payload SigV4. Same wire
// shape as PutStreaming but scoped to a single part within an active
// multipart upload.
func (u *S3Uploader) uploadPartStreaming(ctx context.Context, endpoint *url.URL, objKey, uploadID string, partNum int, body io.Reader, size int64) (string, error) {
	q := url.Values{}
	q.Set("partNumber", strconv.Itoa(partNum))
	q.Set("uploadId", uploadID)
	uri := u.objectURL(endpoint, objKey, q)

	now := time.Now().UTC()
	date := now.Format("20060102")
	credentialScope := date + "/" + u.cfg.Region + "/s3/aws4_request"

	encLen := streamingEncodedLen(size)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uri.String(), nil)
	if err != nil {
		return "", err
	}
	req.ContentLength = encLen
	req.Header.Set("Content-Encoding", "aws-chunked")
	req.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	req.Header.Set("X-Amz-Decoded-Content-Length", strconv.FormatInt(size, 10))
	req.Header.Set("X-Amz-Content-Sha256", streamingPayloadType)
	req.Header.Set("Host", uri.Host)

	signedHeaders, canonicalHeaders := canonicalHeaders(req)
	canonicalReq := strings.Join([]string{
		req.Method, canonicalURI(req.URL), canonicalQuery(req.URL),
		canonicalHeaders, signedHeaders, streamingPayloadType,
	}, "\n")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		now.Format("20060102T150405Z"),
		credentialScope,
		hexHash(canonicalReq),
	}, "\n")
	signingKey := deriveSigningKey(u.cfg.SecretKey, date, u.cfg.Region, "s3")
	seedSig := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential="+u.cfg.AccessKey+"/"+credentialScope+
			", SignedHeaders="+signedHeaders+", Signature="+seedSig)

	req.Body = io.NopCloser(newStreamingChunkReader(
		body, signingKey, credentialScope,
		now.Format("20060102T150405Z"), seedSig,
	))
	resp, err := u.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("part %d: %s — %s", partNum, resp.Status, string(msg))
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		return "", errors.New("part response missing ETag")
	}
	return etag, nil
}

