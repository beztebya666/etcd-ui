package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yourorg/etcd-ui/internal/config"
	"github.com/yourorg/etcd-ui/internal/tracing"
)

// PatroniSource queries one or more Patroni REST endpoints and infers the
// underlying DCS (etcd) endpoints from /cluster responses.
type PatroniSource struct {
	URLs   []string
	Client *http.Client
}

func NewPatroniSource(urls []string) *PatroniSource {
	return &PatroniSource{
		URLs: urls,
		Client: &http.Client{
			Timeout:   5 * time.Second,
			Transport: tracing.WrapTransport(http.DefaultTransport),
		},
	}
}

func (p *PatroniSource) Name() string { return "patroni" }

type patroniMember struct {
	Name    string `json:"name"`
	APIURL  string `json:"api_url"`
	Host    string `json:"host"`
	Role    string `json:"role"`
}

type patroniCluster struct {
	Scope   string          `json:"scope"`
	Members []patroniMember `json:"members"`
	DCSLastSeen any         `json:"dcs_last_seen,omitempty"`
	// Some Patroni builds embed dcs info; we mostly use scope as the cluster name.
}

func (p *PatroniSource) Discover(ctx context.Context) ([]config.Cluster, error) {
	if len(p.URLs) == 0 {
		return nil, nil
	}
	var firstErr error
	for _, u := range p.URLs {
		c, err := p.fetch(ctx, u)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		// Patroni tells us about Postgres nodes, not etcd. The convention
		// is that etcd typically lives on the same hosts as Patroni nodes
		// on port 2379. Operators can override via PATRONI_ETCD_PORT.
		port := "2379"
		etcdEndpoints := make([]string, 0, len(c.Members))
		for _, m := range c.Members {
			host := m.Host
			if host == "" && m.APIURL != "" {
				// strip "http(s)://host:port" → host
				h := strings.TrimPrefix(strings.TrimPrefix(m.APIURL, "http://"), "https://")
				if i := strings.IndexAny(h, ":/"); i >= 0 {
					h = h[:i]
				}
				host = h
			}
			if host == "" {
				continue
			}
			etcdEndpoints = append(etcdEndpoints, fmt.Sprintf("http://%s:%s", host, port))
		}
		if len(etcdEndpoints) == 0 {
			continue
		}
		id := "patroni-" + c.Scope
		return []config.Cluster{{
			ID:        id,
			Name:      "patroni:" + c.Scope,
			Endpoints: etcdEndpoints,
			Source:    "patroni",
		}}, nil
	}
	if firstErr == nil {
		firstErr = errors.New("no Patroni endpoints reachable")
	}
	return nil, firstErr
}

func (p *PatroniSource) fetch(ctx context.Context, base string) (*patroniCluster, error) {
	u := strings.TrimRight(base, "/") + "/cluster"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("patroni %s: %s", u, resp.Status)
	}
	var pc patroniCluster
	if err := json.NewDecoder(resp.Body).Decode(&pc); err != nil {
		return nil, err
	}
	return &pc, nil
}
