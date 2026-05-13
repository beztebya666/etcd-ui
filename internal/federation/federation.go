// Package federation lets a single etcd-ui "hub" surface clusters that live
// on remote etcd-ui peers. Each peer continues to manage its own pool, auth
// and audit — the hub only aggregates the *list* and proxies on-demand.
//
// Configure via env:
//
//	ETCD_UI_PEERS=https://eu.etcd-ui.example.com,https://us.etcd-ui.example.com
//	ETCD_UI_PEER_TOKEN=Bearer abc123…    (optional shared token sent in Authorization)
//	ETCD_UI_PEER_INTERVAL=30s            (default)
//
// Endpoints exposed by the hub:
//
//	GET /api/federation/peers    — peer health snapshot
//	GET /api/federation/clusters — flat list of cluster summaries, with `peer`
//	                               field so the UI knows where each one lives.
//
// Cluster IDs are namespaced `<peer-id>/<cluster-id>` to avoid collisions
// across sites.

package federation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yourorg/etcd-ui/internal/models"
	"github.com/yourorg/etcd-ui/internal/tracing"

	"go.uber.org/zap"
)

type Peer struct {
	ID          string                  `json:"id"`
	URL         string                  `json:"url"`
	Reachable   bool                    `json:"reachable"`
	LastChecked time.Time               `json:"lastChecked"`
	Error       string                  `json:"error,omitempty"`
	Clusters    []models.ClusterSummary `json:"clusters,omitempty"`
	// Aggregate health derived from the peer's clusters.
	// Reachable peer with all-clusters-healthy → "healthy".
	// Reachable peer with at least one unhealthy cluster → "degraded".
	// Unreachable peer → "down".
	// Reachable peer with no clusters → "empty".
	Health string `json:"health"`
	// Counts so the hub UI can render "3/5 clusters healthy" without
	// iterating the (potentially large) Clusters slice client-side.
	ClustersTotal     int `json:"clustersTotal"`
	ClustersHealthy   int `json:"clustersHealthy"`
	ClustersUnhealthy int `json:"clustersUnhealthy"`
}

type Federation struct {
	log    *zap.Logger
	token  string // static bearer (legacy/dev)
	client *http.Client

	// OIDC client-credentials service account. When configured, every peer
	// request swaps `token` for a freshly-minted (and cached) bearer.
	oidc *oidcSA

	mu    sync.RWMutex
	peers map[string]*Peer
}

// oidcSA mints client_credentials tokens against the configured IdP and
// caches them until ~30s before expiry. Concurrent callers share one mint.
type oidcSA struct {
	issuer       string
	clientID     string
	clientSecret string
	scope        string
	client       *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time

	tokenEndpoint string // discovered + cached
}

func NewFromEnv(log *zap.Logger) *Federation {
	raw := os.Getenv("ETCD_UI_PEERS")
	if raw == "" {
		return nil
	}
	f := &Federation{
		log:   log,
		token: os.Getenv("ETCD_UI_PEER_TOKEN"),
		peers: map[string]*Peer{},
		client: &http.Client{
			Timeout:   5 * time.Second,
			Transport: tracing.WrapTransport(http.DefaultTransport),
		},
	}
	for _, u := range strings.Split(raw, ",") {
		u = strings.TrimSpace(strings.TrimRight(u, "/"))
		if u == "" {
			continue
		}
		f.peers[idForURL(u)] = &Peer{ID: idForURL(u), URL: u}
	}
	// OIDC service-account is opt-in. Reuses the OIDC issuer the main auth
	// surface is already configured for, so peers can validate the token via
	// their own existing ETCD_UI_OIDC_* setup.
	if os.Getenv("ETCD_UI_PEER_OIDC") == "on" {
		f.oidc = &oidcSA{
			issuer:       strings.TrimRight(os.Getenv("ETCD_UI_OIDC_ISSUER"), "/"),
			clientID:     os.Getenv("ETCD_UI_PEER_OIDC_CLIENT_ID"),
			clientSecret: os.Getenv("ETCD_UI_PEER_OIDC_SECRET"),
			scope:        firstNonEmpty(os.Getenv("ETCD_UI_PEER_OIDC_SCOPE"), "etcd-ui.peer"),
			client: &http.Client{
				Timeout:   5 * time.Second,
				Transport: tracing.WrapTransport(http.DefaultTransport),
			},
		}
		if f.oidc.issuer == "" || f.oidc.clientID == "" || f.oidc.clientSecret == "" {
			log.Warn("ETCD_UI_PEER_OIDC=on but issuer/client_id/secret are missing — falling back to static token")
			f.oidc = nil
		}
	}
	return f
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// authorize fills the Authorization header on a peer request. Prefers the
// OIDC service-account token; falls back to the static bearer; falls back
// to no auth.
//
// onBehalfOf, when non-empty, also propagates the calling human's identity
// in X-Etcd-UI-On-Behalf — see ApplyOnBehalf on the peer side. Lets the
// peer enforce real per-user ACL rules instead of trusting blanket peer
// access.
func (f *Federation) authorize(ctx context.Context, req *http.Request, onBehalfOf string) error {
	if f.oidc != nil {
		tok, err := f.oidc.get(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	} else if f.token != "" {
		v := f.token
		if !strings.HasPrefix(strings.ToLower(v), "bearer ") {
			v = "Bearer " + v
		}
		req.Header.Set("Authorization", v)
	}
	if onBehalfOf != "" {
		req.Header.Set("X-Etcd-UI-On-Behalf", onBehalfOf)
	}
	return nil
}

func (s *oidcSA) get(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && time.Until(s.expires) > 30*time.Second {
		return s.token, nil
	}
	if s.tokenEndpoint == "" {
		ep, err := s.discover(ctx)
		if err != nil {
			return "", err
		}
		s.tokenEndpoint = ep
	}
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", s.clientID)
	form.Set("client_secret", s.clientSecret)
	form.Set("scope", s.scope)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenEndpoint,
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Error != "" {
		return "", errors.New(body.Error + ": " + body.ErrorDesc)
	}
	if body.AccessToken == "" {
		return "", errors.New("empty access_token")
	}
	s.token = body.AccessToken
	ttl := time.Duration(body.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	s.expires = time.Now().Add(ttl)
	return s.token, nil
}

func (s *oidcSA) discover(ctx context.Context) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		s.issuer+"/.well-known/openid-configuration", nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var disc struct {
		TokenEndpoint string `json:"token_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&disc); err != nil {
		return "", err
	}
	if disc.TokenEndpoint == "" {
		return "", errors.New("discovery missing token_endpoint")
	}
	return disc.TokenEndpoint, nil
}

// idForURL strips scheme + path and uses host as the peer id.
func idForURL(u string) string {
	s := u
	for _, p := range []string{"https://", "http://"} {
		s = strings.TrimPrefix(s, p)
	}
	if i := strings.Index(s, "/"); i > 0 {
		s = s[:i]
	}
	return s
}

// Watch refreshes peer state every `period`. Blocks on ctx.
func (f *Federation) Watch(ctx context.Context, period time.Duration) {
	if period <= 0 {
		period = 30 * time.Second
		if v := os.Getenv("ETCD_UI_PEER_INTERVAL"); v != "" {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				period = d
			}
		}
	}
	f.refresh(ctx)
	t := time.NewTicker(period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f.refresh(ctx)
		}
	}
}

func (f *Federation) refresh(ctx context.Context) {
	f.mu.RLock()
	peers := make([]*Peer, 0, len(f.peers))
	for _, p := range f.peers {
		peers = append(peers, p)
	}
	f.mu.RUnlock()

	var wg sync.WaitGroup
	for _, p := range peers {
		wg.Add(1)
		go func(p *Peer) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			clusters, err := f.fetchPeer(pctx, p.URL)
			f.mu.Lock()
			p.LastChecked = time.Now().UTC()
			if err != nil {
				p.Reachable = false
				p.Error = err.Error()
				p.Clusters = nil
				p.Health = "down"
				p.ClustersTotal, p.ClustersHealthy, p.ClustersUnhealthy = 0, 0, 0
			} else {
				p.Reachable = true
				p.Error = ""
				ns := make([]models.ClusterSummary, len(clusters))
				healthy, unhealthy := 0, 0
				for i, c := range clusters {
					c.ID = p.ID + "/" + c.ID
					ns[i] = c
					if c.Healthy {
						healthy++
					} else {
						unhealthy++
					}
				}
				p.Clusters = ns
				p.ClustersTotal = len(ns)
				p.ClustersHealthy = healthy
				p.ClustersUnhealthy = unhealthy
				switch {
				case len(ns) == 0:
					p.Health = "empty"
				case unhealthy > 0:
					p.Health = "degraded"
				default:
					p.Health = "healthy"
				}
			}
			f.mu.Unlock()
		}(p)
	}
	wg.Wait()
}

func (f *Federation) fetchPeer(ctx context.Context, base string) ([]models.ClusterSummary, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/clusters", nil)
	// Background refresh — no human in the loop; propagate "system" as the
	// on-behalf user so peer ACL can distinguish background discovery from
	// interactive proxied requests.
	if err := f.authorize(ctx, req, "system:hub-poll"); err != nil {
		return nil, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, errors.New(resp.Status)
	}
	var out []models.ClusterSummary
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// Snapshot returns a copy of current peer state for the /federation/peers
// endpoint.
func (f *Federation) Snapshot() []Peer {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]Peer, 0, len(f.peers))
	for _, p := range f.peers {
		out = append(out, *p)
	}
	return out
}

// AggregateClusters concatenates every reachable peer's namespaced clusters.
func (f *Federation) AggregateClusters() []models.ClusterSummary {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []models.ClusterSummary
	for _, p := range f.peers {
		if !p.Reachable {
			continue
		}
		out = append(out, p.Clusters...)
	}
	return out
}
