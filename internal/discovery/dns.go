package discovery

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/yourorg/etcd-ui/internal/config"
)

// DNSSource resolves SRV records to etcd endpoints. This is the recommended
// discovery mode for production etcd clusters that follow the standard
// `_etcd-client._tcp.<domain>` naming, including:
//
//   - kubeadm clusters with external etcd configured via `--discovery-srv`
//   - HashiCorp Vault when the etcd backend is configured with `discovery_srv`
//   - Generic deployments that publish SRV records
//
// Each entry in ETCD_UI_DNS_SRV is "id=name=record[,scheme]". Example:
//
//	ETCD_UI_DNS_SRV="prod=production=_etcd-client._tcp.prod.example.com,https \
//	                ;dev=staging=_etcd-client._tcp.dev.example.com,http"
type DNSSource struct {
	specs []dnsSpec
}

type dnsSpec struct {
	id, name, record, scheme string
}

func NewDNSSource() *DNSSource {
	raw := os.Getenv("ETCD_UI_DNS_SRV")
	if raw == "" {
		return &DNSSource{}
	}
	var specs []dnsSpec
	for _, item := range strings.Split(raw, ";") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, "=", 3)
		if len(parts) < 3 {
			continue
		}
		recordAndScheme := strings.SplitN(parts[2], ",", 2)
		scheme := "https"
		if len(recordAndScheme) == 2 {
			scheme = strings.TrimSpace(recordAndScheme[1])
		}
		specs = append(specs, dnsSpec{
			id:     strings.TrimSpace(parts[0]),
			name:   strings.TrimSpace(parts[1]),
			record: strings.TrimSpace(recordAndScheme[0]),
			scheme: scheme,
		})
	}
	return &DNSSource{specs: specs}
}

func (d *DNSSource) Name() string { return "dns-srv" }

func (d *DNSSource) Discover(ctx context.Context) ([]config.Cluster, error) {
	if len(d.specs) == 0 {
		return nil, nil
	}
	var out []config.Cluster
	var resolver = net.DefaultResolver
	for _, s := range d.specs {
		// LookupSRV automatically prepends _service._proto if first two args
		// are non-empty, OR treats the third arg as the full FQDN if first
		// two are empty. We use full-FQDN form.
		_, addrs, err := resolver.LookupSRV(ctx, "", "", s.record)
		if err != nil || len(addrs) == 0 {
			continue
		}
		eps := make([]string, 0, len(addrs))
		for _, a := range addrs {
			host := strings.TrimSuffix(a.Target, ".")
			eps = append(eps, fmt.Sprintf("%s://%s:%d", s.scheme, host, a.Port))
		}
		out = append(out, config.Cluster{
			ID:        s.id,
			Name:      s.name,
			Endpoints: eps,
			Source:    "dns-srv",
			CAFile:    os.Getenv("ETCD_CA_FILE"),
			CertFile:  os.Getenv("ETCD_CERT_FILE"),
			KeyFile:   os.Getenv("ETCD_KEY_FILE"),
		})
	}
	return out, nil
}
