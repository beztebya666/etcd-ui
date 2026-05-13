package etcdpool

import (
	"context"
	"errors"
	"time"

	"github.com/yourorg/etcd-ui/internal/models"
)

// summaryV2 builds a ClusterSummary using only the v2 HTTP API. db size /
// raft term / alarms aren't exposed by v2 — fields stay zero with a note.
func (p *Pool) summaryV2(ctx context.Context, id string) (models.ClusterSummary, error) {
	p.mu.RLock()
	c, ok := p.clusters[id]
	v2cli := p.v2Clients[id]
	p.mu.RUnlock()
	if !ok || v2cli == nil {
		return models.ClusterSummary{}, errors.New("cluster not connected (v2)")
	}
	s := models.ClusterSummary{
		ID:          c.ID,
		Name:        c.Name,
		Source:      c.Source,
		Endpoints:   c.Endpoints,
		LastChecked: time.Now().UTC(),
		MemberCount: len(c.Endpoints),
		Alarms:      []string{"etcd v2 — limited features"},
		APIVersion:  "v2",
	}
	// Trivial reachability check.
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := v2cli.Get(cctx, "/", false); err != nil {
		s.Error = err.Error()
		return s, nil
	}
	s.Healthy = true
	return s, nil
}
