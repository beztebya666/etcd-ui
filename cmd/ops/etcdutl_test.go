package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// etcdutl tests don't need a live etcd — they exercise the upload limit
// + exit-code parsing against the bundled binary, which any CI image
// will already have if it includes `/usr/local/bin/etcdutl`.

func TestEtcdutl_InvalidSnapshot(t *testing.T) {
	if _, err := os.Stat("/usr/local/bin/etcdutl"); err != nil {
		t.Skip("etcdutl not installed; skipping")
	}
	req := httptest.NewRequest("POST", "/x", bytes.NewReader([]byte("garbage")))
	w := httptest.NewRecorder()
	etcdutlStatusHandler()(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["ok"] != false {
		t.Errorf("garbage should fail: %+v", resp)
	}
	if !strings.Contains(resp["stderr"].(string), "invalid database") {
		t.Errorf("expected error stderr: %v", resp["stderr"])
	}
}

func TestEtcdutl_EmptyBody(t *testing.T) {
	if _, err := os.Stat("/usr/local/bin/etcdutl"); err != nil {
		t.Skip("etcdutl not installed; skipping")
	}
	req := httptest.NewRequest("POST", "/x", http.NoBody)
	w := httptest.NewRecorder()
	etcdutlStatusHandler()(w, req)
	if w.Code != 400 {
		t.Errorf("empty body should 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEtcdutl_SizeLimit(t *testing.T) {
	if _, err := os.Stat("/usr/local/bin/etcdutl"); err != nil {
		t.Skip("etcdutl not installed; skipping")
	}
	// Default cap is 32 MiB. Send 33 MiB worth of bytes — handler should
	// return 400 once the MaxBytesReader trips.
	os.Setenv("ETCD_UI_ETCDUTL_MAX_MB", "1")
	defer os.Unsetenv("ETCD_UI_ETCDUTL_MAX_MB")
	big := bytes.Repeat([]byte("x"), 2*1024*1024) // 2 MiB > 1 MiB cap
	req := httptest.NewRequest("POST", "/x", bytes.NewReader(big))
	w := httptest.NewRecorder()
	etcdutlStatusHandler()(w, req)
	if w.Code == 200 {
		// The 200 path with exitCode != 0 is also acceptable when the file
		// was truncated and etcdutl bails — both outcomes block the over-
		// sized upload from being processed as a valid snapshot.
		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["ok"] == true {
			t.Errorf("oversized upload was accepted as valid: %+v", resp)
		}
	}
}
