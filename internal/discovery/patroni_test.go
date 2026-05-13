package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPatroniDiscover(t *testing.T) {
	body := patroniCluster{
		Scope: "stack-a",
		Members: []patroniMember{
			{Name: "n1", Host: "10.0.0.1"},
			{Name: "n2", APIURL: "http://10.0.0.2:8008"},
			{Name: "n3"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cluster" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	s := NewPatroniSource([]string{srv.URL})
	cs, err := s.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(cs) != 1 {
		t.Fatalf("want 1 cluster, got %d", len(cs))
	}
	if cs[0].ID != "patroni-stack-a" || cs[0].Source != "patroni" {
		t.Fatalf("bad cluster: %+v", cs[0])
	}
	if len(cs[0].Endpoints) != 2 {
		t.Fatalf("want 2 endpoints (n1+n2; n3 has no host), got %v", cs[0].Endpoints)
	}
}

func TestPatroniDiscoverNoURLs(t *testing.T) {
	s := NewPatroniSource(nil)
	cs, err := s.Discover(context.Background())
	if err != nil || cs != nil {
		t.Fatalf("want no error and nil result, got err=%v cs=%v", err, cs)
	}
}
