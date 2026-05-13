// Package peer lets the kv/ops services pull the live cluster registry from
// the cluster service over loopback HTTP. That way, clusters added at runtime
// (manual, k8s, patroni discovery) propagate to every microservice without a
// shared database.
package peer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/yourorg/etcd-ui/internal/config"
	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/tracing"
	"go.uber.org/zap"
)

type ReadonlyApplier interface {
	Set(ids []string)
}

type Syncer struct {
	URL      string
	Pool     *etcdpool.Pool
	Readonly ReadonlyApplier
	Log      *zap.Logger
	Period   time.Duration

	mu     sync.Mutex
	known  map[string]struct{}
	client *http.Client
}

func NewSyncer(url string, pool *etcdpool.Pool, log *zap.Logger) *Syncer {
	return &Syncer{
		URL:    url,
		Pool:   pool,
		Log:    log,
		Period: 30 * time.Second,
		known: map[string]struct{}{},
		client: &http.Client{
			Timeout:   5 * time.Second,
			Transport: tracing.WrapTransport(http.DefaultTransport),
		},
	}
}

// SetReadonlyApplier wires the syncer to a ReadonlyGuard. After each sync the
// list of cluster IDs marked .ReadOnly is propagated to the guard.
func (s *Syncer) SetReadonlyApplier(r ReadonlyApplier) { s.Readonly = r }

func (s *Syncer) Run(ctx context.Context) {
	_ = s.Sync(ctx)
	t := time.NewTicker(s.Period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = s.Sync(ctx)
		}
	}
}

func (s *Syncer) Sync(ctx context.Context) error {
	if s.URL == "" {
		return errors.New("peer url empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL+"/internal/clusters", nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.Log.Warn("peer sync failed", zap.Error(err))
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("peer sync: %s", resp.Status)
	}
	var clusters []config.Cluster
	if err := json.NewDecoder(resp.Body).Decode(&clusters); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	next := map[string]struct{}{}
	readonly := []string{}
	for _, c := range clusters {
		next[c.ID] = struct{}{}
		if c.ReadOnly {
			readonly = append(readonly, c.ID)
		}
		if _, ok := s.known[c.ID]; ok {
			continue
		}
		if err := s.Pool.Upsert(c); err != nil {
			s.Log.Warn("upsert cluster", zap.String("id", c.ID), zap.Error(err))
			continue
		}
	}
	for id := range s.known {
		if _, ok := next[id]; !ok {
			s.Pool.Remove(id)
		}
	}
	s.known = next
	if s.Readonly != nil {
		s.Readonly.Set(readonly)
	}
	return nil
}
