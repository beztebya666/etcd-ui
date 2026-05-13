package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yourorg/etcd-ui/internal/config"
	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/models"
)

// Integration tests for the put-cas endpoint. Requires a real etcd v3
// running at ETCD_UI_TEST_ENDPOINT (CI sets this to a sidecar; locally
// you can point at any throwaway etcd). Tests are skipped when no
// endpoint is configured, so `go test ./...` stays clean in environments
// without etcd.

func mkPool(t *testing.T) (*etcdpool.Pool, string) {
	t.Helper()
	ep := os.Getenv("ETCD_UI_TEST_ENDPOINT")
	if ep == "" {
		t.Skip("set ETCD_UI_TEST_ENDPOINT to run integration tests against real etcd")
	}
	clusterID := fmt.Sprintf("cas-test-%d", time.Now().UnixNano())
	pool := etcdpool.New(5 * time.Second)
	if err := pool.Upsert(config.Cluster{
		ID:        clusterID,
		Name:      clusterID,
		Endpoints: []string{ep},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool, clusterID
}

func mkRouter(pool *etcdpool.Pool) *chi.Mux {
	r := chi.NewRouter()
	r.Post("/clusters/{id}/put-cas", casHandler(pool))
	return r
}

func doCAS(t *testing.T, r http.Handler, clusterID string, body models.PutCASRequest) models.PutCASResponse {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/clusters/"+clusterID+"/put-cas", bytes.NewReader(b))
	req.Header.Set("content-type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp models.PutCASResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode (status %d): %v\nbody: %s", w.Code, err, w.Body.String())
	}
	if w.Code >= 500 {
		t.Fatalf("server error %d: %s", w.Code, w.Body.String())
	}
	return resp
}

func TestCAS_NewKey(t *testing.T) {
	pool, clusterID := mkPool(t)
	r := mkRouter(pool)
	key := fmt.Sprintf("/test/cas/new-%d", time.Now().UnixNano())
	resp := doCAS(t, r, clusterID, models.PutCASRequest{Key: key, Value: "v1", BaseRev: 0})
	if resp.Status != "ok" {
		t.Fatalf("expected ok, got %+v", resp)
	}
	if resp.Revision == 0 {
		t.Error("expected non-zero revision")
	}
}

func TestCAS_NoContention(t *testing.T) {
	pool, clusterID := mkPool(t)
	r := mkRouter(pool)
	key := fmt.Sprintf("/test/cas/nocont-%d", time.Now().UnixNano())

	// First write.
	r1 := doCAS(t, r, clusterID, models.PutCASRequest{Key: key, Value: "v1", BaseRev: 0})
	if r1.Status != "ok" {
		t.Fatalf("first put: %+v", r1)
	}
	// Second write with correct baseRev: should succeed cleanly.
	r2 := doCAS(t, r, clusterID, models.PutCASRequest{Key: key, Value: "v2", BaseRev: r1.Revision})
	if r2.Status != "ok" {
		t.Fatalf("second put: %+v", r2)
	}
	if r2.Revision <= r1.Revision {
		t.Errorf("revision didn't advance: %d -> %d", r1.Revision, r2.Revision)
	}
}

func TestCAS_CleanMerge(t *testing.T) {
	// Same key changed by two writers, non-overlapping lines.
	// Our edit changes line 2, theirs changes line 4.
	pool, clusterID := mkPool(t)
	r := mkRouter(pool)
	key := fmt.Sprintf("/test/cas/merge-%d", time.Now().UnixNano())

	base := "alpha\nbeta\ngamma\ndelta\nepsilon\n"
	r0 := doCAS(t, r, clusterID, models.PutCASRequest{Key: key, Value: base, BaseRev: 0})
	if r0.Status != "ok" {
		t.Fatalf("seed: %+v", r0)
	}

	// "theirs" writes first (changes delta -> DELTA-NEW). Uses correct baseRev.
	their := "alpha\nbeta\ngamma\nDELTA-NEW\nepsilon\n"
	r1 := doCAS(t, r, clusterID, models.PutCASRequest{Key: key, Value: their, BaseRev: r0.Revision})
	if r1.Status != "ok" {
		t.Fatalf("their put: %+v", r1)
	}

	// "ours" attempts with the ORIGINAL baseRev (didn't see their write).
	// Changes beta -> BETA-NEW. Should clean-merge.
	ours := "alpha\nBETA-NEW\ngamma\ndelta\nepsilon\n"
	r2 := doCAS(t, r, clusterID, models.PutCASRequest{Key: key, Value: ours, BaseRev: r0.Revision})
	if r2.Status != "merged" {
		t.Fatalf("expected clean merge, got %+v", r2)
	}
	if !strings.Contains(r2.Merged, "BETA-NEW") || !strings.Contains(r2.Merged, "DELTA-NEW") {
		t.Errorf("merge lost an edit: %q", r2.Merged)
	}
}

func TestCAS_Conflict(t *testing.T) {
	// Same line edited by both writers — should produce a conflict.
	pool, clusterID := mkPool(t)
	r := mkRouter(pool)
	key := fmt.Sprintf("/test/cas/conflict-%d", time.Now().UnixNano())

	r0 := doCAS(t, r, clusterID, models.PutCASRequest{Key: key, Value: "line-A\n", BaseRev: 0})
	if r0.Status != "ok" {
		t.Fatalf("seed: %+v", r0)
	}
	r1 := doCAS(t, r, clusterID, models.PutCASRequest{Key: key, Value: "line-B\n", BaseRev: r0.Revision})
	if r1.Status != "ok" {
		t.Fatalf("their put: %+v", r1)
	}
	// Our write with stale baseRev, different edit on same line.
	r2 := doCAS(t, r, clusterID, models.PutCASRequest{Key: key, Value: "line-C\n", BaseRev: r0.Revision})
	if r2.Status != "conflict" {
		t.Fatalf("expected conflict, got %+v", r2)
	}
	if !strings.Contains(r2.Merged, "<<<<<<<") || !strings.Contains(r2.Merged, ">>>>>>>") {
		t.Errorf("expected conflict markers: %q", r2.Merged)
	}
	if r2.Theirs != "line-B\n" {
		t.Errorf("theirs not surfaced: %q", r2.Theirs)
	}
}

func TestCAS_AcceptConflicts(t *testing.T) {
	// Caller resolves a conflict and resubmits with AcceptConflicts=true —
	// the server writes verbatim, even with markers, since the user signed off.
	pool, clusterID := mkPool(t)
	r := mkRouter(pool)
	key := fmt.Sprintf("/test/cas/accept-%d", time.Now().UnixNano())

	resolved := "user-resolved-content\n"
	resp := doCAS(t, r, clusterID, models.PutCASRequest{
		Key:             key,
		Value:           resolved,
		BaseRev:         0,
		AcceptConflicts: true,
	})
	if resp.Status != "ok" {
		t.Fatalf("expected ok with AcceptConflicts, got %+v", resp)
	}

	// Even markers go through with AcceptConflicts.
	keyM := fmt.Sprintf("/test/cas/accept-markers-%d", time.Now().UnixNano())
	withMarkers := "<<<<<<< ours\nA\n=======\nB\n>>>>>>> theirs\n"
	resp2 := doCAS(t, r, clusterID, models.PutCASRequest{
		Key:             keyM,
		Value:           withMarkers,
		BaseRev:         0,
		AcceptConflicts: true,
	})
	if resp2.Status != "ok" {
		t.Fatalf("expected ok for marker-bearing value, got %+v", resp2)
	}
}

// Smoke test: many concurrent CAS attempts on the same key. The retry
// loop should ensure exactly one of them wins per round and the others
// either merge cleanly or report conflict — but none should silently
// drop their write or hang.
func TestCAS_Concurrent(t *testing.T) {
	pool, clusterID := mkPool(t)
	r := mkRouter(pool)
	key := fmt.Sprintf("/test/cas/race-%d", time.Now().UnixNano())

	r0 := doCAS(t, r, clusterID, models.PutCASRequest{Key: key, Value: "seed\n", BaseRev: 0})
	if r0.Status != "ok" {
		t.Fatalf("seed: %+v", r0)
	}

	const N = 8
	type outcome struct{ status string }
	results := make(chan outcome, N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			resp := doCAS(t, r, clusterID, models.PutCASRequest{
				Key:     key,
				Value:   fmt.Sprintf("writer-%d\n", i),
				BaseRev: r0.Revision,
			})
			results <- outcome{status: resp.Status}
		}()
	}
	got := map[string]int{}
	for i := 0; i < N; i++ {
		select {
		case o := <-results:
			got[o.status]++
		case <-time.After(15 * time.Second):
			t.Fatalf("concurrent CAS hung at iteration %d (have %+v)", i, got)
		}
	}
	// Some combination of ok/merged/conflict — none of them should be 0 of all-ok
	// since only one writer can win the first txn cleanly.
	if got["ok"]+got["merged"]+got["conflict"] != N {
		t.Errorf("unexpected status distribution: %+v", got)
	}
}

// Ensures context cancellation aborts the retry loop instead of looping forever.
func TestCAS_ContextCancel(t *testing.T) {
	pool, clusterID := mkPool(t)
	r := mkRouter(pool)
	key := fmt.Sprintf("/test/cas/cancel-%d", time.Now().UnixNano())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already-cancelled
	b, _ := json.Marshal(models.PutCASRequest{Key: key, Value: "x", BaseRev: 0})
	req := httptest.NewRequestWithContext(ctx, "POST", "/clusters/"+clusterID+"/put-cas", bytes.NewReader(b))
	req.Header.Set("content-type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == 200 {
		t.Errorf("expected non-200 on cancelled context; got 200 body %s", w.Body.String())
	}
}
