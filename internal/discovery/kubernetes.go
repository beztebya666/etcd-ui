package discovery

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/yourorg/etcd-ui/internal/config"
	"github.com/yourorg/etcd-ui/internal/tracing"
)

// K8sSelector is one named pod-label probe. Multiple selectors are supported
// so a single etcd-ui instance can discover etcd from many independent systems
// at once (control-plane + Vitess + Cilium + KubeEdge + Karmada + custom).
type K8sSelector struct {
	// ID/Name shown in the UI. Required and unique.
	ID            string `json:"id"`
	Name          string `json:"name"`
	Namespace     string `json:"namespace"`     // "" → all namespaces (cluster-wide list)
	LabelSelector string `json:"labelSelector"` // e.g. "app=etcd"
	Port          int    `json:"port"`          // defaults 2379
	Scheme        string `json:"scheme"`        // http | https (default https for control-plane, else http)
	// Optional client cert overrides on a per-selector basis.
	CAFile   string `json:"caFile,omitempty"`
	CertFile string `json:"certFile,omitempty"`
	KeyFile  string `json:"keyFile,omitempty"`
}

// presetSelectors covers the most common deployments out of the box. They are
// all *opt-in* — only run if labels actually exist on a pod in the cluster, so
// adding them is free.
var presetSelectors = []K8sSelector{
	// Kubernetes control plane (stacked etcd).
	{ID: "k8s-control-plane", Name: "kubernetes:control-plane",
		Namespace: "kube-system", LabelSelector: "component=etcd",
		Port: 2379, Scheme: "https"},

	// Vitess (TopoServer is etcd). https://vitess.io
	{ID: "vitess-topo", Name: "vitess:topo",
		Namespace: "", LabelSelector: "planetscale.com/component=etcd",
		Port: 2379, Scheme: "http"},

	// Cilium operator-deployed etcd. https://docs.cilium.io
	{ID: "cilium-etcd", Name: "cilium:kvstore",
		Namespace: "", LabelSelector: "io.cilium/app=etcd-operator",
		Port: 2379, Scheme: "http"},

	// KubeEdge cloudcore etcd
	{ID: "kubeedge-etcd", Name: "kubeedge:etcd",
		Namespace: "", LabelSelector: "k8s-app=kubeedge-etcd",
		Port: 2379, Scheme: "http"},

	// Karmada control plane etcd
	{ID: "karmada-etcd", Name: "karmada:etcd",
		Namespace: "karmada-system", LabelSelector: "app=etcd",
		Port: 2379, Scheme: "https"},

	// Apache APISIX (configures itself against an etcd cluster, typically deployed alongside)
	{ID: "apisix-etcd", Name: "apisix:etcd",
		Namespace: "", LabelSelector: "app.kubernetes.io/name=etcd,app.kubernetes.io/part-of=apisix",
		Port: 2379, Scheme: "http"},

	// M3DB (placement / kv layer is etcd)
	{ID: "m3db-etcd", Name: "m3db:etcd",
		Namespace: "", LabelSelector: "app=etcd,part-of=m3db",
		Port: 2379, Scheme: "http"},

	// Generic catch-all: any pod labelled "app=etcd".
	{ID: "generic-app-etcd", Name: "generic:app=etcd",
		Namespace: "", LabelSelector: "app=etcd",
		Port: 2379, Scheme: "http"},
}

// KubernetesSource performs in-cluster (or kubeconfig-free) HTTPS probes to
// the Kubernetes API and resolves matching pods to etcd endpoints.
type KubernetesSource struct {
	client    *http.Client
	selectors []K8sSelector
}

func NewKubernetesSource() *KubernetesSource {
	return &KubernetesSource{
		client: &http.Client{
			Timeout:   5 * time.Second,
			Transport: tracing.WrapTransport(http.DefaultTransport),
		},
		selectors: loadSelectors(),
	}
}

func (k *KubernetesSource) Name() string { return "kubernetes" }

// loadSelectors returns presetSelectors + any user-defined selectors from
// ETCD_UI_K8S_SOURCES (a JSON array of K8sSelector), with user selectors
// taking precedence (matched by ID).
func loadSelectors() []K8sSelector {
	all := append([]K8sSelector{}, presetSelectors...)
	if raw := os.Getenv("ETCD_UI_K8S_SOURCES"); raw != "" {
		var extra []K8sSelector
		if err := json.Unmarshal([]byte(raw), &extra); err == nil {
			seen := map[string]int{}
			for i, s := range all {
				seen[s.ID] = i
			}
			for _, s := range extra {
				if i, ok := seen[s.ID]; ok {
					all[i] = s // override
					continue
				}
				all = append(all, s)
			}
		}
	}
	return all
}

func (k *KubernetesSource) Discover(ctx context.Context) ([]config.Cluster, error) {
	// Explicit endpoints win over all of this.
	if v := os.Getenv("ETCD_UI_K8S_ENDPOINTS"); v != "" {
		eps := splitNonEmpty(v, ",")
		if len(eps) == 0 {
			return nil, nil
		}
		return []config.Cluster{{
			ID:        "k8s-explicit",
			Name:      "kubernetes:explicit",
			Endpoints: eps,
			Source:    "kubernetes",
		}}, nil
	}

	cli, host, port, token, err := inClusterClient()
	if err != nil || cli == nil {
		return nil, err // not in-cluster, silent skip
	}

	var out []config.Cluster
	for _, sel := range k.selectors {
		eps, err := k.lookup(ctx, cli, host, port, token, sel)
		if err != nil || len(eps) == 0 {
			continue
		}
		out = append(out, config.Cluster{
			ID:        sel.ID,
			Name:      sel.Name,
			Endpoints: eps,
			Source:    "kubernetes",
			CAFile:    coalesce(sel.CAFile, os.Getenv("ETCD_CA_FILE")),
			CertFile:  coalesce(sel.CertFile, os.Getenv("ETCD_CERT_FILE")),
			KeyFile:   coalesce(sel.KeyFile, os.Getenv("ETCD_KEY_FILE")),
		})
	}
	return out, nil
}

func (k *KubernetesSource) lookup(
	ctx context.Context, cli *http.Client, host, port, token string, sel K8sSelector,
) ([]string, error) {
	path := "/api/v1/pods"
	if sel.Namespace != "" {
		path = "/api/v1/namespaces/" + sel.Namespace + "/pods"
	}
	url := fmt.Sprintf("https://%s:%s%s?labelSelector=%s", host, port, path, sel.LabelSelector)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("k8s list pods: %s", resp.Status)
	}
	var body struct {
		Items []struct {
			Metadata struct{ Name string } `json:"metadata"`
			Status   struct{ PodIP string } `json:"status"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	scheme := sel.Scheme
	if scheme == "" {
		scheme = "http"
	}
	clientPort := sel.Port
	if clientPort == 0 {
		clientPort = 2379
	}
	var eps []string
	for _, it := range body.Items {
		if it.Status.PodIP == "" {
			continue
		}
		eps = append(eps, fmt.Sprintf("%s://%s:%d", scheme, it.Status.PodIP, clientPort))
	}
	return eps, nil
}

// inClusterClient returns a TLS-enabled HTTP client wired up with the
// in-cluster service-account token, plus the API host/port/token. Returns
// (nil,...) without error if not running in-cluster.
func inClusterClient() (*http.Client, string, string, string, error) {
	tokenPath := "/var/run/secrets/kubernetes.io/serviceaccount/token"
	caPath := "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	tokB, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, "", "", "", nil // not in-cluster
	}
	caB, err := os.ReadFile(caPath)
	if err != nil {
		return nil, "", "", "", err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caB) {
		return nil, "", "", "", fmt.Errorf("k8s ca: invalid PEM")
	}
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, "", "", "", nil
	}
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: tracing.WrapTransport(&http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		}),
	}, host, port, strings.TrimSpace(string(tokB)), nil
}

func splitNonEmpty(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
