package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yourorg/etcd-ui/internal/config"
	"github.com/yourorg/etcd-ui/internal/etcdpool"
)

func mkLockPool(t *testing.T) (*etcdpool.Pool, string) {
	t.Helper()
	ep := os.Getenv("ETCD_UI_TEST_ENDPOINT")
	if ep == "" {
		t.Skip("set ETCD_UI_TEST_ENDPOINT to run integration tests against real etcd")
	}
	id := fmt.Sprintf("lock-test-%d", time.Now().UnixNano())
	pool := etcdpool.New(5 * time.Second)
	if err := pool.Upsert(config.Cluster{ID: id, Name: id, Endpoints: []string{ep}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool, id
}

func mkLockRouter(pool *etcdpool.Pool) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/clusters/{id}/locks", locksListHandler(pool))
	r.Post("/clusters/{id}/locks/acquire", lockAcquireHandler(pool))
	r.Delete("/clusters/{id}/locks/{leaseId}", lockReleaseHandler(pool))
	return r
}

// acquireResp mirrors lockAcquireHandler's response struct. We use a
// typed decode (rather than map[string]any) because lease IDs are 64-bit
// integers — JSON numbers in Go's default decoder become float64, which
// loses precision on the top 11 bits. The bug surfaces only when the
// lease ID happens to land in that range, which is silently flaky.
type acquireResp struct {
	LeaseID    int64  `json:"leaseId"`
	Key        string `json:"key"`
	Acquired   bool   `json:"acquired"`
	Holder     string `json:"holder"`
	Waiters    int    `json:"waiters"`
	TTLSeconds int64  `json:"ttlSeconds"`
}

func acquire(t *testing.T, r http.Handler, clusterID, prefix, tag string, ttl int64) acquireResp {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"prefix":     prefix,
		"holderTag":  tag,
		"ttlSeconds": ttl,
	})
	req := httptest.NewRequest("POST", "/clusters/"+clusterID+"/locks/acquire", bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("acquire %d: %s", w.Code, w.Body.String())
	}
	var out acquireResp
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func release(t *testing.T, r http.Handler, clusterID string, leaseID int64) {
	t.Helper()
	req := httptest.NewRequest("DELETE", "/clusters/"+clusterID+"/locks/"+strconv.FormatInt(leaseID, 10), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Fatalf("release %d: %s", w.Code, w.Body.String())
	}
}

func list(t *testing.T, r http.Handler, clusterID, prefix string) map[string]any {
	t.Helper()
	req := httptest.NewRequest("GET", "/clusters/"+clusterID+"/locks?prefix="+prefix, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("list %d: %s", w.Code, w.Body.String())
	}
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

func TestLocks_Ordering(t *testing.T) {
	pool, clusterID := mkLockPool(t)
	r := mkLockRouter(pool)
	prefix := fmt.Sprintf("/locks/test-ordering-%d/", time.Now().UnixNano())

	a := acquire(t, r, clusterID, prefix, "alice", 60)
	if !a.Acquired {
		t.Fatalf("alice should hold the lock: %+v", a)
	}
	if a.Waiters != 0 {
		t.Errorf("alice waiters: %d", a.Waiters)
	}

	b := acquire(t, r, clusterID, prefix, "bob", 60)
	if b.Acquired {
		t.Errorf("bob shouldn't hold yet: %+v", b)
	}
	if b.Holder != "alice" {
		t.Errorf("holder field not alice: %q", b.Holder)
	}
	if b.Waiters != 1 {
		t.Errorf("bob should see 1 waiter (himself): %d", b.Waiters)
	}

	c := acquire(t, r, clusterID, prefix, "carol", 60)
	if c.Acquired || c.Waiters != 2 {
		t.Errorf("carol should be 3rd in queue: %+v", c)
	}

	l := list(t, r, clusterID, prefix)
	entries := l["entries"].([]any)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Release alice → bob takes over.
	release(t, r, clusterID, a.LeaseID)
	time.Sleep(150 * time.Millisecond)
	l2 := list(t, r, clusterID, prefix)
	e2 := l2["entries"].([]any)
	if len(e2) != 2 {
		t.Fatalf("expected 2 entries after release, got %d", len(e2))
	}

	// Cleanup.
	release(t, r, clusterID, b.LeaseID)
	release(t, r, clusterID, c.LeaseID)
}

func TestLocks_TTLClamped(t *testing.T) {
	pool, clusterID := mkLockPool(t)
	r := mkLockRouter(pool)
	prefix := fmt.Sprintf("/locks/ttl-%d/", time.Now().UnixNano())

	// Server clamps TTL to [1, 3600]. Negative → default 60.
	a := acquire(t, r, clusterID, prefix, "x", -5)
	if a.TTLSeconds != 60 {
		t.Errorf("negative TTL not normalised to default: %d", a.TTLSeconds)
	}
	release(t, r, clusterID, a.LeaseID)

	// Above ceiling → also default.
	b := acquire(t, r, clusterID, prefix, "y", 99999)
	if b.TTLSeconds != 60 {
		t.Errorf("oversized TTL not clamped: %d", b.TTLSeconds)
	}
	release(t, r, clusterID, b.LeaseID)
}

func TestLocks_ReleaseUnknownLease(t *testing.T) {
	pool, clusterID := mkLockPool(t)
	r := mkLockRouter(pool)
	// Revoking a never-issued lease should fail cleanly, not panic.
	req := httptest.NewRequest("DELETE", "/clusters/"+clusterID+"/locks/9999999999999999", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == 200 || w.Code == 204 {
		t.Errorf("expected error for unknown lease, got %d", w.Code)
	}
}
