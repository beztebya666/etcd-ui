package etcdpool

import (
	"context"
	"sort"
	"strings"

	"github.com/yourorg/etcd-ui/internal/models"

	clientv2 "go.etcd.io/etcd/client/v2"
)

// Helpers that translate the v2 protocol into the v3-shaped models the rest of
// the codebase consumes. v2 has no MVCC — there's no revision-walk, no prevKv
// on watches, no leases, no txn. Anything that requires those returns
// ErrNotSupportedV2 and the HTTP layer maps it to 501.

// Range walks a prefix/from-key on a v2 cluster and returns a flat slice of
// leaf nodes. The v2 protocol returns a tree (directories with .Nodes); we
// flatten it so the UI sees the same shape it gets from v3.
//
// Arguments mirror the subset of v3 we actually use:
//   - key:       full key or prefix (empty → "/")
//   - withPrefix: walk recursively under `key`
//   - limit:     soft client-side cap on returned KVs (0 = unbounded)
func (v *V2Client) Range(ctx context.Context, key string, withPrefix bool, limit int64) ([]models.KV, error) {
	if key == "" {
		key = "/"
	}
	resp, err := v.api.Get(ctx, key, &clientv2.GetOptions{Recursive: withPrefix, Sort: true})
	if err != nil {
		return nil, err
	}
	out := make([]models.KV, 0, 64)
	collectV2(resp.Node, &out)
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	if limit > 0 && int64(len(out)) > limit {
		out = out[:limit]
	}
	return out, nil
}

func collectV2(n *clientv2.Node, into *[]models.KV) {
	if n == nil {
		return
	}
	if n.Dir {
		for _, child := range n.Nodes {
			collectV2(child, into)
		}
		return
	}
	*into = append(*into, models.KV{
		Key:            n.Key,
		Value:          n.Value,
		CreateRevision: int64(n.CreatedIndex),
		ModRevision:    int64(n.ModifiedIndex),
		Version:        int64(n.ModifiedIndex - n.CreatedIndex + 1),
	})
}

// Watch wraps a v2 watcher and ferries events into a channel until ctx is
// cancelled. Closes ch on return.
func (v *V2Client) Watch(ctx context.Context, key string, ch chan<- models.WatchEvent) error {
	defer close(ch)
	w := v.api.Watcher(key, &clientv2.WatcherOptions{Recursive: true})
	for {
		resp, err := w.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if resp == nil || resp.Node == nil {
			continue
		}
		ev := models.WatchEvent{
			Type:     v2EventType(resp.Action),
			Key:      resp.Node.Key,
			Value:    resp.Node.Value,
			Revision: int64(resp.Node.ModifiedIndex),
		}
		if resp.PrevNode != nil {
			ev.PrevValue = resp.PrevNode.Value
		}
		select {
		case <-ctx.Done():
			return nil
		case ch <- ev:
		}
	}
}

func v2EventType(action string) string {
	switch strings.ToLower(action) {
	case "delete", "expire", "compareanddelete":
		return "DELETE"
	default:
		return "PUT"
	}
}
