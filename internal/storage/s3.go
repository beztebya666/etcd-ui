// Package storage implements a tiny dependency-free S3-compatible PUT client
// used to offload scheduled snapshots off local disk. Works with:
//
//   - AWS S3
//   - MinIO / Ceph RGW (any S3-API endpoint)
//   - Google Cloud Storage via the S3-compat XML API
//     (https://storage.googleapis.com, set HMAC key as access/secret)
//   - Cloudflare R2 / Wasabi / Backblaze B2 (S3-compat mode)
//
// We deliberately skip the AWS SDK — it's a 30 MB+ dependency for one PUT.
// SigV4 is small enough to inline. Single-PUT only; objects > 5 GiB require
// multipart and aren't in our use case (etcd snapshot files for typical
// clusters are sub-GB).
//
// Config from env:
//
//	ETCD_UI_SNAPSHOT_S3_ENDPOINT=https://s3.amazonaws.com  (or https://storage.googleapis.com)
//	ETCD_UI_SNAPSHOT_S3_REGION=us-east-1
//	ETCD_UI_SNAPSHOT_S3_BUCKET=etcd-backups
//	ETCD_UI_SNAPSHOT_S3_PREFIX=clusters/                   (optional; default "")
//	ETCD_UI_SNAPSHOT_S3_ACCESS_KEY=AKIA…
//	ETCD_UI_SNAPSHOT_S3_SECRET_KEY=…
//	ETCD_UI_SNAPSHOT_S3_FORCE_PATH_STYLE=1                 (MinIO-style; default 0)

package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type S3Config struct {
	Endpoint       string
	Region         string
	Bucket         string
	Prefix         string
	AccessKey      string
	SecretKey      string
	ForcePathStyle bool
}

// S3FromEnv returns a configured S3 uploader or nil if the env vars are not
// set (caller treats nil as "S3 offload disabled").
func S3FromEnv() *S3Uploader {
	cfg := S3Config{
		Endpoint:       strings.TrimRight(os.Getenv("ETCD_UI_SNAPSHOT_S3_ENDPOINT"), "/"),
		Region:         os.Getenv("ETCD_UI_SNAPSHOT_S3_REGION"),
		Bucket:         os.Getenv("ETCD_UI_SNAPSHOT_S3_BUCKET"),
		Prefix:         os.Getenv("ETCD_UI_SNAPSHOT_S3_PREFIX"),
		AccessKey:      os.Getenv("ETCD_UI_SNAPSHOT_S3_ACCESS_KEY"),
		SecretKey:      os.Getenv("ETCD_UI_SNAPSHOT_S3_SECRET_KEY"),
		ForcePathStyle: os.Getenv("ETCD_UI_SNAPSHOT_S3_FORCE_PATH_STYLE") != "",
	}
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	return &S3Uploader{cfg: cfg, client: &http.Client{Timeout: 10 * time.Minute}}
}

type S3Uploader struct {
	cfg    S3Config
	client *http.Client
}

func (u *S3Uploader) Endpoint() string { return u.cfg.Endpoint }
func (u *S3Uploader) Bucket() string   { return u.cfg.Bucket }

// Put streams a file to s3://bucket/<prefix><key> with SigV4 auth. The body
// is hashed inline (necessary for SigV4 — there's no "unsigned-payload"
// streaming with stock signing on PUT). For multi-GB objects we'd need to
// switch to chunked-payload signing; out of scope here.
func (u *S3Uploader) Put(ctx context.Context, key string, body io.ReadSeeker, contentType string) error {
	// Hash full body for SigV4.
	hash := sha256.New()
	if _, err := io.Copy(hash, body); err != nil {
		return fmt.Errorf("hash body: %w", err)
	}
	payloadHash := hex.EncodeToString(hash.Sum(nil))
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind body: %w", err)
	}
	size, err := body.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("size body: %w", err)
	}
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind body: %w", err)
	}

	objKey := strings.TrimLeft(u.cfg.Prefix+key, "/")
	u2, err := url.Parse(u.cfg.Endpoint)
	if err != nil {
		return err
	}
	if u.cfg.ForcePathStyle {
		u2.Path = "/" + u.cfg.Bucket + "/" + objKey
	} else {
		u2.Host = u.cfg.Bucket + "." + u2.Host
		u2.Path = "/" + objKey
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u2.String(), body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	req.Header.Set("Content-Type", contentType)
	now := time.Now().UTC()
	req.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("Host", u2.Host)

	if err := signV4(req, payloadHash, now, u.cfg.Region, "s3", u.cfg.AccessKey, u.cfg.SecretKey); err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("s3 put %s: %s — %s", objKey, resp.Status, string(msg))
	}
	return nil
}

// --- SigV4 implementation (PUT only; query-string params not supported) ---

func signV4(req *http.Request, payloadHash string, t time.Time, region, service, ak, sk string) error {
	date := t.Format("20060102")
	credentialScope := date + "/" + region + "/" + service + "/aws4_request"

	signedHeaderNames, canonicalHeaders := canonicalHeaders(req)
	canonicalReq := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		canonicalQuery(req.URL),
		canonicalHeaders,
		signedHeaderNames,
		payloadHash,
	}, "\n")

	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		t.Format("20060102T150405Z"),
		credentialScope,
		hexHash(canonicalReq),
	}, "\n")

	signingKey := hmacSHA256(hmacSHA256(hmacSHA256(hmacSHA256(
		[]byte("AWS4"+sk), []byte(date)), []byte(region)), []byte(service)), []byte("aws4_request"))
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		ak, credentialScope, signedHeaderNames, signature,
	))
	return nil
}

func canonicalHeaders(req *http.Request) (string, string) {
	names := make([]string, 0, len(req.Header)+1)
	values := map[string]string{}
	for k, v := range req.Header {
		lk := strings.ToLower(k)
		if lk == "host" || lk == "content-type" || strings.HasPrefix(lk, "x-amz-") {
			names = append(names, lk)
			values[lk] = strings.TrimSpace(strings.Join(v, ","))
		}
	}
	if _, ok := values["host"]; !ok {
		names = append(names, "host")
		values["host"] = req.URL.Host
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteString(":")
		b.WriteString(values[n])
		b.WriteString("\n")
	}
	return strings.Join(names, ";"), b.String()
}

func canonicalURI(u *url.URL) string {
	if u.Path == "" {
		return "/"
	}
	return u.EscapedPath()
}

func canonicalQuery(u *url.URL) string {
	q := u.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		for _, v := range q[k] {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

func hexHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ErrNotConfigured is returned by helpers when callers expect an uploader but
// env wasn't set.
var ErrNotConfigured = errors.New("S3 offload not configured")
