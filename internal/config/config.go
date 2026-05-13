package config

import (
	"os"
	"strings"
	"time"
)

// Cluster represents a single etcd cluster connection profile loaded from env.
type Cluster struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Endpoints []string `json:"endpoints"`
	Username  string   `json:"username,omitempty"`
	Password  string   `json:"-"`
	CAFile    string   `json:"caFile,omitempty"`
	CertFile  string   `json:"certFile,omitempty"`
	KeyFile   string   `json:"keyFile,omitempty"`
	Source    string   `json:"source"`   // env | k8s | patroni | manual | file | dns-srv
	ReadOnly  bool     `json:"readOnly,omitempty"`
}

// Config is the shared runtime config consumed by every microservice.
type Config struct {
	GatewayAddr string
	ClusterAddr string
	KVAddr      string
	OpsAddr     string
	AuditAddr   string
	WebRoot     string
	DataDir     string

	DialTimeout  time.Duration
	WatchTimeout time.Duration

	// Statically configured clusters (from env). Discovery may add more at runtime.
	Clusters []Cluster

	// Auto-discovery toggles
	KubernetesDiscovery bool
	PatroniURLs         []string

	// ReadonlyClusters: cluster IDs that the kv/ops services must refuse
	// to mutate, regardless of the per-cluster ReadOnly flag.
	ReadonlyClusters []string
}

func get(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func Load() *Config {
	c := &Config{
		GatewayAddr:         get("ETCD_UI_GATEWAY_ADDR", ":8080"),
		ClusterAddr:         get("ETCD_UI_CLUSTER_ADDR", "127.0.0.1:7001"),
		KVAddr:              get("ETCD_UI_KV_ADDR", "127.0.0.1:7002"),
		OpsAddr:             get("ETCD_UI_OPS_ADDR", "127.0.0.1:7003"),
		AuditAddr:           get("ETCD_UI_AUDIT_ADDR", "127.0.0.1:7004"),
		WebRoot:             get("ETCD_UI_WEB_ROOT", "web/dist"),
		DataDir:             get("ETCD_UI_DATA_DIR", "/app/data"),
		DialTimeout:         5 * time.Second,
		WatchTimeout:        0,
		KubernetesDiscovery: get("ETCD_UI_K8S_DISCOVERY", "auto") != "off",
		PatroniURLs:         splitCSV(os.Getenv("PATRONI_URLS")),
	}

	if eps := splitCSV(os.Getenv("ETCD_ENDPOINTS")); len(eps) > 0 {
		readonly := os.Getenv("ETCD_READONLY") == "true" || os.Getenv("ETCD_READONLY") == "1"
		c.Clusters = append(c.Clusters, Cluster{
			ID:        get("ETCD_UI_CLUSTER_ID", "default"),
			Name:      get("ETCD_UI_CLUSTER_NAME", "default"),
			Endpoints: eps,
			Username:  os.Getenv("ETCD_USERNAME"),
			Password:  os.Getenv("ETCD_PASSWORD"),
			CAFile:    os.Getenv("ETCD_CA_FILE"),
			CertFile:  os.Getenv("ETCD_CERT_FILE"),
			KeyFile:   os.Getenv("ETCD_KEY_FILE"),
			Source:    "env",
			ReadOnly:  readonly,
		})
	}

	// Comma-separated list of cluster IDs that should be treated read-only
	// no matter their source. Useful for "production" clusters.
	if v := os.Getenv("ETCD_UI_READONLY_CLUSTERS"); v != "" {
		c.ReadonlyClusters = splitCSV(v)
	}

	return c
}
