package etcdpool

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/yourorg/etcd-ui/internal/config"
	"github.com/yourorg/etcd-ui/internal/models"

	pkgtls "go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// (helpers are stable across this file: ProbeVersion/V2Client live in version.go
//  and v2.go respectively.)

// Pool keeps one *clientv3.Client per cluster id. It is safe for concurrent use.
// A background goroutine inspects cert file mtimes; on change it re-dials the
// affected cluster so cert rotation doesn't need a process restart.
type Pool struct {
	dialTimeout time.Duration
	mu          sync.RWMutex
	clusters    map[string]config.Cluster
	clients     map[string]*clientv3.Client
	v2Clients   map[string]*V2Client // populated for clusters running etcd 2.x
	apis        map[string]ServerAPI // detected API per cluster
	certMTimes  map[string]time.Time // path -> last seen mtime
}

func New(dialTimeout time.Duration) *Pool {
	return &Pool{
		dialTimeout: dialTimeout,
		clusters:    map[string]config.Cluster{},
		clients:     map[string]*clientv3.Client{},
		v2Clients:   map[string]*V2Client{},
		apis:        map[string]ServerAPI{},
		certMTimes:  map[string]time.Time{},
	}
}

// APIVersion returns the detected etcd API the cluster speaks.
func (p *Pool) APIVersion(id string) ServerAPI {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.apis[id]
}

// V2 returns the v2 client for clusters detected as APIv2, or nil otherwise.
func (p *Pool) V2(id string) *V2Client {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.v2Clients[id]
}

// WatchCerts periodically (every 30s) re-checks the mtime of any cert files in
// the pool. Anything that changed → re-dial that cluster. Safe to call once
// from the cluster service.
func (p *Pool) WatchCerts(ctx context.Context) {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.scanCerts()
			}
		}
	}()
}

func (p *Pool) scanCerts() {
	p.mu.RLock()
	clusters := make([]config.Cluster, 0, len(p.clusters))
	for _, c := range p.clusters {
		clusters = append(clusters, c)
	}
	p.mu.RUnlock()

	for _, c := range clusters {
		changed := false
		for _, path := range []string{c.CAFile, c.CertFile, c.KeyFile} {
			if path == "" {
				continue
			}
			st, err := os.Stat(path)
			if err != nil {
				continue
			}
			p.mu.Lock()
			if prev, ok := p.certMTimes[path]; !ok {
				p.certMTimes[path] = st.ModTime()
			} else if !st.ModTime().Equal(prev) {
				p.certMTimes[path] = st.ModTime()
				changed = true
			}
			p.mu.Unlock()
		}
		if changed {
			_ = p.Upsert(c) // redial with same config
		}
	}
}

func (p *Pool) Upsert(c config.Cluster) error {
	if c.ID == "" {
		return errors.New("cluster id is required")
	}

	// Detect API version before we attempt to dial. Cheap (one GET /version).
	probeCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	api, version, perr := ProbeVersion(probeCtx, c)

	switch api {
	case APIv1:
		return fmt.Errorf(
			"etcd %s is too old (v1, EOL since 2014). Upgrade or use a tunnel/proxy that translates v1→v3",
			version)
	case APIv2:
		v2cli, err := NewV2Client(c)
		if err != nil {
			return fmt.Errorf("v2 dial: %w", err)
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if old, ok := p.clients[c.ID]; ok {
			_ = old.Close()
			delete(p.clients, c.ID)
		}
		p.clusters[c.ID] = c
		p.v2Clients[c.ID] = v2cli
		p.apis[c.ID] = APIv2
		return nil
	}
	// APIv3 (or unknown — most modern deployments answer /version with 3.x; if
	// the probe failed (perr != nil) we still try v3 since that's the default).
	_ = perr
	cli, err := p.dial(c)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if old, ok := p.clients[c.ID]; ok {
		_ = old.Close()
	}
	delete(p.v2Clients, c.ID)
	p.clusters[c.ID] = c
	p.clients[c.ID] = cli
	if api == APIUnknown {
		p.apis[c.ID] = APIv3
	} else {
		p.apis[c.ID] = api
	}
	return nil
}

func (p *Pool) Remove(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if old, ok := p.clients[id]; ok {
		_ = old.Close()
	}
	delete(p.clients, id)
	delete(p.v2Clients, id)
	delete(p.apis, id)
	delete(p.clusters, id)
}

func (p *Pool) Client(id string) (*clientv3.Client, config.Cluster, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	cli, ok := p.clients[id]
	if !ok {
		return nil, config.Cluster{}, fmt.Errorf("cluster %q not connected", id)
	}
	return cli, p.clusters[id], nil
}

func (p *Pool) List() []config.Cluster {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]config.Cluster, 0, len(p.clusters))
	for _, c := range p.clusters {
		out = append(out, c)
	}
	return out
}

func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, cli := range p.clients {
		_ = cli.Close()
	}
	p.clients = map[string]*clientv3.Client{}
}

func (p *Pool) dial(c config.Cluster) (*clientv3.Client, error) {
	cfg := clientv3.Config{
		Endpoints:   c.Endpoints,
		DialTimeout: p.dialTimeout,
		Username:    c.Username,
		Password:    c.Password,
	}
	if c.CAFile != "" || c.CertFile != "" || c.KeyFile != "" {
		tlsInfo := pkgtls.TLSInfo{
			TrustedCAFile: c.CAFile,
			CertFile:      c.CertFile,
			KeyFile:       c.KeyFile,
		}
		t, err := tlsInfo.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("tls config: %w", err)
		}
		cfg.TLS = t
	} else if hasScheme(c.Endpoints, "https") {
		cfg.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return clientv3.New(cfg)
}

func hasScheme(eps []string, scheme string) bool {
	prefix := scheme + "://"
	for _, e := range eps {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// Summary inspects the cluster: members, leader, db size, alarms.
func (p *Pool) Summary(ctx context.Context, id string) (models.ClusterSummary, error) {
	// v2 clusters answer a different surface — fall back to a minimal probe.
	if p.APIVersion(id) == APIv2 {
		return p.summaryV2(ctx, id)
	}

	cli, c, err := p.Client(id)
	if err != nil {
		return models.ClusterSummary{}, err
	}
	s := models.ClusterSummary{
		ID:          c.ID,
		Name:        c.Name,
		Source:      c.Source,
		Endpoints:   c.Endpoints,
		LastChecked: time.Now().UTC(),
		APIVersion:  "v3",
	}

	mctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	ml, err := cli.MemberList(mctx)
	if err != nil {
		s.Error = err.Error()
		return s, nil
	}
	s.MemberCount = len(ml.Members)

	for _, ep := range c.Endpoints {
		st, err := cli.Status(mctx, ep)
		if err != nil {
			continue
		}
		s.Healthy = true
		s.DBSizeBytes = st.DbSize
		s.DBSizeInUse = st.DbSizeInUse
		s.Revision = st.Header.Revision
		s.RaftTerm = st.RaftTerm
		s.LeaderID = st.Leader
		s.ServerVersion = st.Version
		// Compute a leader display string that strips the common DNS
		// suffix shared with peers — turns e.g.
		//   node-aa-1.internal.example (+ siblings on same domain)
		// into just `node-aa-1`. We keep the full name in `Leader` so
		// hover/copy is unambiguous; SPA shows whichever fits.
		names := make([]string, 0, len(ml.Members))
		for _, m := range ml.Members {
			names = append(names, m.Name)
		}
		commonSuffix := longestCommonDotSuffix(names)
		for _, m := range ml.Members {
			if m.ID == st.Leader {
				s.Leader = m.Name
				if commonSuffix != "" && len(m.Name) > len(commonSuffix)+1 {
					s.LeaderShort = m.Name[:len(m.Name)-len(commonSuffix)]
				}
			}
		}
		// Number of keys currently in the keyspace. Cheap — etcd returns
		// it from MVCC without scanning. Useful as an "is the cluster
		// empty" cue alongside revision/db size.
		if cnt, err := cli.Get(mctx, "\x00", clientv3.WithFromKey(), clientv3.WithCountOnly()); err == nil {
			s.KeyCount = cnt.Count
		}
		break
	}

	al, err := cli.AlarmList(mctx)
	if err == nil {
		for _, a := range al.Alarms {
			s.Alarms = append(s.Alarms, a.Alarm.String())
		}
	}
	return s, nil
}

// longestCommonDotSuffix returns the longest trailing substring that
// starts at a dot boundary and is shared by every element of names. The
// leading dot is included in the return so callers can subtract its
// length to slice. Returns "" when no shared dot-suffix exists or input
// has fewer than 2 names. Robust to mixed-case (DNS is case-insensitive
// but we keep the original case from etcd).
func longestCommonDotSuffix(names []string) string {
	if len(names) < 2 {
		return ""
	}
	// Reverse-scan character-by-character to find common suffix.
	short := names[0]
	for _, n := range names[1:] {
		if len(n) < len(short) {
			short = n
		}
	}
	end := len(short)
	for i := 1; i <= end; i++ {
		ch := short[len(short)-i]
		for _, n := range names {
			if n[len(n)-i] != ch {
				// Back up to the previous dot boundary.
				suffix := short[len(short)-(i-1):]
				if dot := indexByteFromStart(suffix, '.'); dot >= 0 {
					return suffix[dot:]
				}
				return ""
			}
		}
	}
	// All names identical.
	return ""
}

func indexByteFromStart(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
