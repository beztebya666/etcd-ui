package etcdpool

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/yourorg/etcd-ui/internal/config"
	"github.com/yourorg/etcd-ui/internal/tracing"
	pkgtls "go.etcd.io/etcd/client/pkg/v3/transport"
)

// ServerAPI describes which etcd HTTP API the cluster speaks.
//
//	v3 — modern gRPC + grpc-gateway (etcd 3.x default). Our primary path.
//	v2 — legacy /v2/keys HTTP API (etcd 2.x, still served by some 3.x setups
//	     when --enable-v2 is true). Fallback for very old deployments.
//	v1 — pre-2014. We refuse to talk; surface a helpful error instead.
type ServerAPI int

const (
	APIUnknown ServerAPI = 0
	APIv1      ServerAPI = 1
	APIv2      ServerAPI = 2
	APIv3      ServerAPI = 3
)

func (a ServerAPI) String() string {
	switch a {
	case APIv3:
		return "v3"
	case APIv2:
		return "v2"
	case APIv1:
		return "v1"
	}
	return "unknown"
}

// ProbeVersion hits GET /version on every endpoint until one answers. etcd
// returns {"etcdserver":"3.5.15","etcdcluster":"3.5.0"}. Anything 3.x → APIv3.
// Anything 2.x → APIv2. 0.x/1.x → APIv1.
func ProbeVersion(ctx context.Context, c config.Cluster) (ServerAPI, string, error) {
	cli := versionClient(c)
	for _, ep := range c.Endpoints {
		api, ver, err := probeOne(ctx, cli, ep)
		if err == nil {
			return api, ver, nil
		}
	}
	return APIUnknown, "", errors.New("no /version reachable on any endpoint")
}

func probeOne(ctx context.Context, cli *http.Client, ep string) (ServerAPI, string, error) {
	url := strings.TrimRight(ep, "/") + "/version"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := cli.Do(req)
	if err != nil {
		return APIUnknown, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return APIUnknown, "", errors.New(resp.Status)
	}
	var body struct {
		EtcdServer  string `json:"etcdserver"`
		EtcdCluster string `json:"etcdcluster"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return APIUnknown, "", err
	}
	v := body.EtcdServer
	if v == "" {
		v = body.EtcdCluster
	}
	switch {
	case strings.HasPrefix(v, "3."):
		return APIv3, v, nil
	case strings.HasPrefix(v, "2."):
		return APIv2, v, nil
	case strings.HasPrefix(v, "1.") || strings.HasPrefix(v, "0."):
		return APIv1, v, nil
	}
	return APIUnknown, v, nil
}

func versionClient(c config.Cluster) *http.Client {
	if c.CAFile != "" || c.CertFile != "" || c.KeyFile != "" {
		info := pkgtls.TLSInfo{TrustedCAFile: c.CAFile, CertFile: c.CertFile, KeyFile: c.KeyFile}
		if tc, err := info.ClientConfig(); err == nil {
			tc.InsecureSkipVerify = true
			return &http.Client{
				Timeout:   3 * time.Second,
				Transport: tracing.WrapTransport(&http.Transport{TLSClientConfig: tc}),
			}
		}
	}
	return &http.Client{
		Timeout: 3 * time.Second,
		Transport: tracing.WrapTransport(&http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
		}),
	}
}
