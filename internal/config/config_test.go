package config

import (
	"os"
	"reflect"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	os.Unsetenv("ETCD_ENDPOINTS")
	os.Unsetenv("PATRONI_URLS")
	c := Load()
	if c.GatewayAddr != ":8080" || c.ClusterAddr != "127.0.0.1:7001" {
		t.Fatalf("defaults wrong: %+v", c)
	}
	if len(c.Clusters) != 0 {
		t.Fatalf("expected no clusters by default, got %d", len(c.Clusters))
	}
}

func TestLoadEnvClusters(t *testing.T) {
	t.Setenv("ETCD_ENDPOINTS", "http://a:2379, http://b:2379")
	t.Setenv("ETCD_UI_CLUSTER_NAME", "ci")
	c := Load()
	if len(c.Clusters) != 1 {
		t.Fatalf("want 1 cluster, got %d", len(c.Clusters))
	}
	got := c.Clusters[0]
	if got.Name != "ci" || got.Source != "env" {
		t.Fatalf("bad cluster: %+v", got)
	}
	wantEPs := []string{"http://a:2379", "http://b:2379"}
	if !reflect.DeepEqual(got.Endpoints, wantEPs) {
		t.Fatalf("endpoints: want %v, got %v", wantEPs, got.Endpoints)
	}
}

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a, b,c ", []string{"a", "b", "c"}},
	}
	for _, c := range cases {
		got := splitCSV(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	// purely-whitespace input returns an (empty, non-nil) slice; just assert len.
	if got := splitCSV(" , "); len(got) != 0 {
		t.Errorf("splitCSV(\" , \") len = %d, want 0", len(got))
	}
}
