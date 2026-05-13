package etcdpool

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"github.com/yourorg/etcd-ui/internal/config"

	pkgtls "go.etcd.io/etcd/client/pkg/v3/transport"
)

func buildV2TLS(c config.Cluster) (*tls.Config, error) {
	if c.CAFile == "" && c.CertFile == "" && c.KeyFile == "" {
		return nil, nil
	}
	info := pkgtls.TLSInfo{TrustedCAFile: c.CAFile, CertFile: c.CertFile, KeyFile: c.KeyFile}
	return info.ClientConfig()
}

// v2Transport is a thin http.RoundTripper used by the v2 client when TLS is
// required. It mirrors clientv2.DefaultTransport's defaults (sane keep-alive)
// plus the TLS config we built from cluster certs.
type v2Transport struct {
	tls *tls.Config
}

func (t *v2Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.transport().RoundTrip(req)
}

// CancelRequest is required by clientv2's CancelableTransport interface.
// We delegate to a fresh underlying *http.Transport — clientv2 only uses this
// path for legacy timeout handling that we already cover via per-request ctx.
func (t *v2Transport) CancelRequest(req *http.Request) {
	t.transport().CancelRequest(req)
}

func (t *v2Transport) transport() *http.Transport {
	return &http.Transport{
		TLSClientConfig: t.tls,
		DialContext: (&net.Dialer{
			Timeout:   3 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
	}
}
