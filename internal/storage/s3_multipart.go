package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Multipart upload for snapshots > 5 GiB. S3 requires parts ≥ 5 MiB except
// the last; we use 64 MiB so a 100 GiB snapshot is ~1600 parts (well under
// the 10000-part hard cap). The whole transfer is serial — we deliberately
// don't parallelise the parts because snapshot offload is a background
// process and we'd rather not double our peak memory footprint.
//
// Sequence:
//
//   POST   <key>?uploads             → returns UploadId
//   PUT    <key>?partNumber=N&uploadId=… (with SigV4 + payload sha)
//   POST   <key>?uploadId=…           with completion XML
//
// On any failure we issue:
//
//   DELETE <key>?uploadId=…           (Abort, frees server-side storage)

const (
	multipartThreshold = 4_500_000_000 // 4.5 GB — switch over before the 5 GiB single-PUT ceiling
	partSize           = 64 * 1024 * 1024
	// parallelUploads is the number of in-flight PUTs during multipart.
	// 4 saturates a typical 1 Gbps uplink on a 64 MiB chunk size without
	// overwhelming the snapshot service's memory budget (4 × 64 MiB = 256
	// MiB peak, vs ~5 GiB local snapshot we're streaming from disk anyway).
	parallelUploads = 4
)

type completedPart struct {
	XMLName    xml.Name `xml:"Part"`
	ETag       string   `xml:"ETag"`
	PartNumber int      `xml:"PartNumber"`
}

type completeMultipartUpload struct {
	XMLName xml.Name        `xml:"CompleteMultipartUpload"`
	Parts   []completedPart `xml:"Part"`
}

type initiateResponse struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	UploadID string   `xml:"UploadId"`
}

// PutLarge picks single-PUT or multipart based on the body size. Callers
// that don't know the size up-front should use this and let it decide.
func (u *S3Uploader) PutLarge(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error {
	if size < multipartThreshold {
		return u.Put(ctx, key, body, contentType)
	}
	return u.putMultipart(ctx, key, body, size, contentType)
}

func (u *S3Uploader) putMultipart(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error {
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
		return fmt.Errorf("initiate multipart: %w", err)
	}
	completed := false
	defer func() {
		if !completed {
			_ = u.abortMultipart(context.Background(), endpoint, objKey, uploadID)
		}
	}()

	// Reader is shared across the producer goroutine; we serialise reads
	// (since *os.File etc. have no parallel read guarantees) and dispatch
	// the bytes to a pool of upload workers. Workers PUT in parallel; the
	// producer side stays single-threaded.
	type job struct {
		num  int
		data []byte
	}
	type result struct {
		num  int
		etag string
		err  error
	}

	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()

	jobs := make(chan job, parallelUploads)
	results := make(chan result, parallelUploads)
	var wg sync.WaitGroup
	for i := 0; i < parallelUploads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				etag, err := u.uploadPart(workerCtx, endpoint, objKey, uploadID, j.num, j.data)
				select {
				case results <- result{num: j.num, etag: etag, err: err}:
				case <-workerCtx.Done():
					return
				}
				if err != nil {
					return
				}
			}
		}()
	}
	// Closer: once producer + all workers are done, close the results chan.
	doneProducing := make(chan struct{})
	go func() {
		<-doneProducing
		close(jobs)
		wg.Wait()
		close(results)
	}()

	// Producer.
	var produceErr error
	go func() {
		defer close(doneProducing)
		for partNum := 1; ; partNum++ {
			buf := make([]byte, partSize)
			n, rerr := io.ReadFull(body, buf)
			if n == 0 && (rerr == io.EOF || rerr == io.ErrUnexpectedEOF) {
				return
			}
			if rerr != nil && rerr != io.EOF && rerr != io.ErrUnexpectedEOF {
				produceErr = rerr
				return
			}
			select {
			case jobs <- job{num: partNum, data: buf[:n]}:
			case <-workerCtx.Done():
				return
			}
			if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
				return
			}
		}
	}()

	parts := make([]completedPart, 0, (size+partSize-1)/partSize)
	for r := range results {
		if r.err != nil {
			cancelWorkers()
			return fmt.Errorf("upload part %d: %w", r.num, r.err)
		}
		parts = append(parts, completedPart{ETag: r.etag, PartNumber: r.num})
	}
	if produceErr != nil {
		return produceErr
	}

	// S3 requires parts in ascending order in the Complete request.
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })

	if err := u.completeMultipart(ctx, endpoint, objKey, uploadID, parts); err != nil {
		return fmt.Errorf("complete multipart: %w", err)
	}
	completed = true
	return nil
}

func (u *S3Uploader) objectURL(endpoint *url.URL, objKey string, query url.Values) *url.URL {
	out := *endpoint
	if u.cfg.ForcePathStyle {
		out.Path = "/" + u.cfg.Bucket + "/" + objKey
	} else {
		out.Host = u.cfg.Bucket + "." + out.Host
		out.Path = "/" + objKey
	}
	out.RawQuery = query.Encode()
	return &out
}

func (u *S3Uploader) signed(req *http.Request, payload []byte) error {
	sum := sha256.Sum256(payload)
	payloadHash := hex.EncodeToString(sum[:])
	now := time.Now().UTC()
	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	return signV4(req, payloadHash, now, u.cfg.Region, "s3", u.cfg.AccessKey, u.cfg.SecretKey)
}

func (u *S3Uploader) initiateMultipart(ctx context.Context, endpoint *url.URL, objKey, contentType string) (string, error) {
	q := url.Values{}
	q.Set("uploads", "")
	uri := u.objectURL(endpoint, objKey, q)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, uri.String(), nil)
	req.Header.Set("Content-Type", contentType)
	if err := u.signed(req, nil); err != nil {
		return "", err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("initiate %s: %s", uri.Path, string(msg))
	}
	var ir initiateResponse
	if err := xml.NewDecoder(resp.Body).Decode(&ir); err != nil {
		return "", err
	}
	if ir.UploadID == "" {
		return "", errors.New("empty UploadId")
	}
	return ir.UploadID, nil
}

func (u *S3Uploader) uploadPart(ctx context.Context, endpoint *url.URL, objKey, uploadID string, partNum int, body []byte) (string, error) {
	q := url.Values{}
	q.Set("partNumber", strconv.Itoa(partNum))
	q.Set("uploadId", uploadID)
	uri := u.objectURL(endpoint, objKey, q)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, uri.String(), bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	if err := u.signed(req, body); err != nil {
		return "", err
	}
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

func (u *S3Uploader) completeMultipart(ctx context.Context, endpoint *url.URL, objKey, uploadID string, parts []completedPart) error {
	body, err := xml.Marshal(completeMultipartUpload{Parts: parts})
	if err != nil {
		return err
	}
	q := url.Values{}
	q.Set("uploadId", uploadID)
	uri := u.objectURL(endpoint, objKey, q)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, uri.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/xml")
	req.ContentLength = int64(len(body))
	if err := u.signed(req, body); err != nil {
		return err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("complete %s: %s", resp.Status, string(msg))
	}
	return nil
}

func (u *S3Uploader) abortMultipart(ctx context.Context, endpoint *url.URL, objKey, uploadID string) error {
	q := url.Values{}
	q.Set("uploadId", uploadID)
	uri := u.objectURL(endpoint, objKey, q)
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, uri.String(), nil)
	if err := u.signed(req, nil); err != nil {
		return err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
