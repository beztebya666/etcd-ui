//go:build integration

// Integration tests exercise the connection pool + Summary code against a
// real etcd. Skip unless ETCD_ENDPOINTS is set.
//
//	ETCD_ENDPOINTS=http://localhost:2379 go test -tags=integration ./internal/etcdpool/...
package etcdpool

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yourorg/etcd-ui/internal/config"
)

func endpoints(t *testing.T) []string {
	v := os.Getenv("ETCD_ENDPOINTS")
	if v == "" {
		t.Skip("set ETCD_ENDPOINTS=<addr> to run integration tests")
	}
	parts := strings.Split(v, ",")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func TestPool_UpsertSummaryClose(t *testing.T) {
	pool := New(5 * time.Second)
	defer pool.Close()
	c := config.Cluster{
		ID:        "it",
		Name:      "it",
		Endpoints: endpoints(t),
		Source:    "env",
	}
	if err := pool.Upsert(c); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	cli, got, err := pool.Client("it")
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if got.ID != "it" || len(cli.Endpoints()) == 0 {
		t.Fatalf("bad client/endpoints: %+v / %v", got, cli.Endpoints())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Put(ctx, "/etcd-ui-it/hello", "world"); err != nil {
		t.Fatalf("put: %v", err)
	}
	t.Cleanup(func() {
		_, _ = cli.Delete(context.Background(), "/etcd-ui-it/", clientWithPrefix())
	})

	r, err := cli.Get(ctx, "/etcd-ui-it/hello")
	if err != nil || len(r.Kvs) != 1 || string(r.Kvs[0].Value) != "world" {
		t.Fatalf("roundtrip mismatch: %v / %+v", err, r.Kvs)
	}

	s, err := pool.Summary(ctx, "it")
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if !s.Healthy || s.MemberCount == 0 {
		t.Fatalf("summary thinks cluster unhealthy: %+v", s)
	}
}
