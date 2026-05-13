package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yourorg/etcd-ui/internal/config"
	"github.com/yourorg/etcd-ui/internal/etcdpool"
)

func mkMoveLeaderPool(t *testing.T) (*etcdpool.Pool, string) {
	t.Helper()
	ep := os.Getenv("ETCD_UI_TEST_ENDPOINT")
	if ep == "" {
		t.Skip("set ETCD_UI_TEST_ENDPOINT")
	}
	id := fmt.Sprintf("ml-test-%d", time.Now().UnixNano())
	pool := etcdpool.New(5 * time.Second)
	if err := pool.Upsert(config.Cluster{ID: id, Name: id, Endpoints: []string{ep}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool, id
}

func mkMoveLeaderRouter(pool *etcdpool.Pool) *chi.Mux {
	r := chi.NewRouter()
	r.Post("/clusters/{id}/move-leader", moveLeaderHandler(pool))
	return r
}

func currentMember(t *testing.T, pool *etcdpool.Pool, clusterID string) uint64 {
	t.Helper()
	cli, _, err := pool.Client(clusterID)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ml, err := cli.MemberList(ctx)
	if err != nil || len(ml.Members) == 0 {
		t.Skip("no members reachable")
	}
	return ml.Members[0].ID
}

// TestMoveLeader_AcceptsStringID — the SPA must pass member IDs as
// JSON strings because uint64 > 2^53 overflows JS Number. Server has
// to accept both forms.
func TestMoveLeader_AcceptsStringID(t *testing.T) {
	pool, clusterID := mkMoveLeaderPool(t)
	r := mkMoveLeaderRouter(pool)
	memberID := currentMember(t, pool, clusterID)
	body := fmt.Sprintf(`{"memberId":"%d"}`, memberID)
	req := httptest.NewRequest("POST", "/clusters/"+clusterID+"/move-leader", bytes.NewReader([]byte(body)))
	req.Header.Set("content-type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == 400 {
		t.Fatalf("string-encoded memberId rejected: %s", w.Body.String())
	}
	// Single-node cluster: target IS the leader → etcd may 200 (no-op
	// transfer) or 502 ("bad leader transferee" — same node). Both fine;
	// what matters is no 400 on the string-encoded ID.
	if w.Code == 200 {
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if _, ok := out["transferredTo"]; !ok {
			t.Errorf("missing transferredTo: %+v", out)
		}
	}
}

func TestMoveLeader_AcceptsNumberID(t *testing.T) {
	pool, clusterID := mkMoveLeaderPool(t)
	r := mkMoveLeaderRouter(pool)
	memberID := currentMember(t, pool, clusterID)
	body := fmt.Sprintf(`{"memberId":%d}`, memberID)
	req := httptest.NewRequest("POST", "/clusters/"+clusterID+"/move-leader", bytes.NewReader([]byte(body)))
	req.Header.Set("content-type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == 400 {
		t.Fatalf("number-encoded memberId rejected: %s", w.Body.String())
	}
}

func TestMoveLeader_RejectsBadInput(t *testing.T) {
	pool, clusterID := mkMoveLeaderPool(t)
	r := mkMoveLeaderRouter(pool)
	for _, body := range []string{`{"memberId":0}`, `{"memberId":"0"}`, `{"memberId":"abc"}`, `{}`} {
		req := httptest.NewRequest("POST", "/clusters/"+clusterID+"/move-leader", bytes.NewReader([]byte(body)))
		req.Header.Set("content-type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Errorf("body %s → %d (expected 400): %s", body, w.Code, w.Body.String())
		}
	}
}

func TestMoveLeader_UnknownCluster(t *testing.T) {
	pool := etcdpool.New(time.Second)
	t.Cleanup(func() { pool.Close() })
	r := mkMoveLeaderRouter(pool)
	req := httptest.NewRequest("POST", "/clusters/nope/move-leader", bytes.NewReader([]byte(`{"memberId":1}`)))
	req.Header.Set("content-type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}
