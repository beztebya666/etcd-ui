//go:build integration

// v2 fallback integration test. Requires an etcd 3.x with --enable-v2 OR an
// actual etcd 2.x. Skip when ETCD_V2_ENDPOINTS isn't set so the default
// integration suite doesn't fail on stock 3.5+ images.
//
//	ETCD_V2_ENDPOINTS=http://localhost:2379 go test -tags=integration ./internal/etcdpool/...

package etcdpool

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yourorg/etcd-ui/internal/config"
)

func v2Endpoints(t *testing.T) []string {
	v := os.Getenv("ETCD_V2_ENDPOINTS")
	if v == "" {
		t.Skip("set ETCD_V2_ENDPOINTS=<addr> to run v2 fallback tests")
	}
	parts := strings.Split(v, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func TestPool_V2_DetectAndRange(t *testing.T) {
	pool := New(5 * time.Second)
	defer pool.Close()
	c := config.Cluster{ID: "v2it", Name: "v2it", Endpoints: v2Endpoints(t)}

	if err := pool.Upsert(c); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if api := pool.APIVersion("v2it"); api != APIv2 {
		t.Fatalf("expected APIv2, got %v — is --enable-v2 set on the target etcd?", api)
	}
	v2 := pool.V2("v2it")
	if v2 == nil {
		t.Fatal("v2 client not registered")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Write something through v2 and read it back via Range.
	if _, err := v2.Set(ctx, "/etcd-ui-it/k", "v"); err != nil {
		t.Fatalf("v2 set: %v", err)
	}
	kvs, err := v2.Range(ctx, "/etcd-ui-it/", true, 0)
	if err != nil {
		t.Fatalf("v2 range: %v", err)
	}
	if len(kvs) == 0 {
		t.Fatal("expected at least one key after v2 Set")
	}

	// Summary should fall through to summaryV2 and not crash on nil v3 client.
	s, err := pool.Summary(ctx, "v2it")
	if err != nil {
		t.Fatalf("summary v2: %v", err)
	}
	if len(s.Alarms) == 0 || !strings.Contains(s.Alarms[0], "limited") {
		t.Fatalf("v2 summary should warn about limited features: %v", s.Alarms)
	}
}
