// Package discovery turns "any cluster anywhere" into a uniform list of
// connection profiles. Three sources are supported:
//
//  1. env — static endpoints from ETCD_ENDPOINTS (always on if set).
//  2. patroni — REST /cluster of one or more Patroni nodes.
//  3. kubernetes — in-cluster probe of kube-system etcd pods (best-effort).
//
// New sources can be added by implementing the Source interface.
package discovery

import (
	"context"
	"sync"
	"time"

	"github.com/yourorg/etcd-ui/internal/config"
	"go.uber.org/zap"
)

type Source interface {
	Name() string
	Discover(ctx context.Context) ([]config.Cluster, error)
}

type Manager struct {
	log     *zap.Logger
	sources []Source
	period  time.Duration

	mu      sync.RWMutex
	results map[string][]config.Cluster // source name -> clusters
}

func NewManager(log *zap.Logger, sources []Source) *Manager {
	return &Manager{
		log:     log,
		sources: sources,
		period:  30 * time.Second,
		results: map[string][]config.Cluster{},
	}
}

func (m *Manager) RunOnce(ctx context.Context) {
	var wg sync.WaitGroup
	for _, s := range m.sources {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			cs, err := s.Discover(ctx)
			if err != nil {
				m.log.Warn("discovery failed", zap.String("source", s.Name()), zap.Error(err))
				return
			}
			m.mu.Lock()
			m.results[s.Name()] = cs
			m.mu.Unlock()
			m.log.Info("discovered", zap.String("source", s.Name()), zap.Int("clusters", len(cs)))
		}(s)
	}
	wg.Wait()
}

func (m *Manager) Run(ctx context.Context) {
	m.RunOnce(ctx)
	t := time.NewTicker(m.period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.RunOnce(ctx)
		}
	}
}

// Flatten returns the union of all discovered clusters with stable IDs.
func (m *Manager) Flatten() []config.Cluster {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := map[string]bool{}
	var out []config.Cluster
	for _, cs := range m.results {
		for _, c := range cs {
			if seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			out = append(out, c)
		}
	}
	return out
}
