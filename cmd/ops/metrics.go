package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	pkgtls "go.etcd.io/etcd/client/pkg/v3/transport"

	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/httpx"
	"github.com/yourorg/etcd-ui/internal/metrics"
	"github.com/yourorg/etcd-ui/internal/tracing"
)

// metricsHandler fetches /metrics from the cluster's etcd processes. Kubeadm
// (and many other distributions) put /metrics on a *separate* port — usually
// 2381 with plain HTTP — set via --listen-metrics-urls. We try, in order:
//
//   1. ETCD_UI_METRICS_URL_<cluster> env override (full URL, comma-sep ok)
//   2. http://host:2381/metrics   (kubeadm default; plain HTTP)
//   3. https://host:2381/metrics  (TLS metrics listener variant)
//   4. https://host:2379/metrics  (etcd also serves /metrics on client port
//      when mTLS is configured — requires our client cert)
//   5. http://host:2379/metrics   (rare; non-TLS deployments)
//
// The first call that returns 2xx with a Prometheus body wins.

var keepMetrics = []string{
	"etcd_server_has_leader",
	"etcd_server_leader_changes_seen_total",
	"etcd_server_proposals_committed_total",
	"etcd_server_proposals_applied_total",
	"etcd_server_proposals_pending",
	"etcd_server_proposals_failed_total",
	"etcd_mvcc_db_total_size_in_bytes",
	"etcd_mvcc_db_total_size_in_use_in_bytes",
	"etcd_disk_wal_fsync_duration_seconds_sum",
	"etcd_disk_wal_fsync_duration_seconds_count",
	"etcd_disk_backend_commit_duration_seconds_sum",
	"etcd_disk_backend_commit_duration_seconds_count",
	"etcd_network_peer_round_trip_time_seconds_sum",
	"etcd_network_peer_round_trip_time_seconds_count",
	"grpc_server_handled_total",
	"grpc_server_started_total",
	"process_resident_memory_bytes",
	"process_cpu_seconds_total",
}

// nodeResult is one entry per cluster endpoint.
type nodeResult struct {
	Endpoint string           `json:"endpoint"`
	Used     string           `json:"used,omitempty"` // actual /metrics URL we hit
	Error    string           `json:"error,omitempty"`
	Samples  []metrics.Sample `json:"samples,omitempty"`
}

func metricsHandler(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		_, c, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		if len(c.Endpoints) == 0 {
			httpx.Err(w, 502, errString("no endpoints"))
			return
		}

		mtlsClient, err := mtlsHTTPClient(c.CAFile, c.CertFile, c.KeyFile)
		if err != nil {
			httpx.Err(w, 500, fmt.Errorf("load tls: %w", err))
			return
		}
		plainClient := &http.Client{
			Timeout: 4 * time.Second,
			Transport: tracing.WrapTransport(&http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
			}),
		}

		nodes := make([]nodeResult, len(c.Endpoints))
		for i, ep := range c.Endpoints {
			res := nodeResult{Endpoint: ep}
			candidates := buildMetricCandidates(id, ep)
			var (
				body    string
				lastErr error
				used    string
			)
			for _, u := range candidates {
				cli := plainClient
				if strings.HasPrefix(u, "https://") && mtlsClient != nil {
					cli = mtlsClient
				}
				if b, ferr := tryFetch(cli, u); ferr == nil {
					body = b
					used = u
					break
				} else {
					lastErr = ferr
				}
			}
			if body == "" {
				res.Error = fmt.Sprintf("no /metrics reachable on %s (last: %v)", ep, lastErr)
			} else {
				res.Used = used
				res.Samples = metrics.Filter(metrics.Parse(strings.NewReader(body)), keepMetrics)
			}
			nodes[i] = res
		}

		httpx.JSON(w, 200, map[string]any{
			"nodes": nodes,
		})
	}
}

func tryFetch(cli *http.Client, u string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("%s -> %s", u, resp.Status)
	}
	const max = 8 << 20 // 8 MiB ceiling — etcd /metrics is typically ~50 KiB.
	b, err := io.ReadAll(io.LimitReader(resp.Body, max))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func buildMetricCandidates(id, endpoint string) []string {
	// Manual override wins. Names are uppercased and non-alnum → '_' so an id
	// of "k8s-prod" becomes ETCD_UI_METRICS_URL_K8S_PROD.
	if v := os.Getenv("ETCD_UI_METRICS_URL_" + sanitizeEnv(id)); v != "" {
		return strings.Split(v, ",")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return []string{strings.TrimRight(endpoint, "/") + "/metrics"}
	}
	host := parsed.Hostname()
	clientPort := parsed.Port()
	if clientPort == "" {
		if parsed.Scheme == "https" {
			clientPort = "2379"
		} else {
			clientPort = "2379"
		}
	}
	// Common kubeadm pattern: metrics on 2381 plain HTTP, even when client is mTLS.
	return []string{
		fmt.Sprintf("http://%s:2381/metrics", host),
		fmt.Sprintf("https://%s:2381/metrics", host),
		fmt.Sprintf("https://%s:%s/metrics", host, clientPort),
		fmt.Sprintf("http://%s:%s/metrics", host, clientPort),
	}
}

func sanitizeEnv(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			if c >= 'a' && c <= 'z' {
				c -= 32
			}
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

// mtlsHTTPClient builds an http.Client using the same cert files configured
// for the etcd client — needed when /metrics lives on the mTLS-protected
// client port. Returns (nil, nil) if no cert files were configured.
func mtlsHTTPClient(caFile, certFile, keyFile string) (*http.Client, error) {
	if certFile == "" && keyFile == "" && caFile == "" {
		return nil, nil
	}
	info := pkgtls.TLSInfo{
		TrustedCAFile: caFile,
		CertFile:      certFile,
		KeyFile:       keyFile,
	}
	tc, err := info.ClientConfig()
	if err != nil {
		return nil, err
	}
	tc.InsecureSkipVerify = true // metrics endpoints' SAN often doesn't match the IP we hit
	return &http.Client{
		Timeout:   4 * time.Second,
		Transport: tracing.WrapTransport(&http.Transport{TLSClientConfig: tc}),
	}, nil
}

type errString string

func (e errString) Error() string { return string(e) }
