package alerts

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// PersistentLog is a tiny append-only ring of cluster events backed by a
// JSONL file. We use it to give the Metrics page a real timeline of
// leader changes / alarm flips / health flips, surviving SPA refreshes
// and process restarts.
//
// Why a separate file from audit:
//   - audit.jsonl records USER actions (PUT, DELETE, RBAC edits). Mixing
//     in autonomous cluster observations would make audit search noisy.
//   - cluster events have a smaller schema (no actor / IP / UA) so the
//     per-row overhead drops 5x.
//
// File is at $ETCD_UI_DATA_DIR/cluster-events.jsonl. Compaction kicks in
// at 5000 lines; we keep the most recent half on rewrite. No locks needed
// across processes — every etcd-ui pod has its own data dir; if you run
// multiple replicas they each maintain their own log (the SPA queries
// the gateway, which queries whichever replica it lands on — leader
// flips are observed cluster-wide so any replica's log is sufficient).
type PersistentLog struct {
	mu    sync.RWMutex
	path  string
	ring  []Event
	max   int
}

func NewPersistentLog(dir string) (*PersistentLog, error) {
	if dir == "" {
		return nil, errors.New("alerts: data dir required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	l := &PersistentLog{
		path: filepath.Join(dir, "cluster-events.jsonl"),
		max:  5000,
	}
	if err := l.load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return l, nil
}

func (l *PersistentLog) load() error {
	f, err := os.Open(l.path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		l.ring = append(l.ring, e)
	}
	return sc.Err()
}

// Append writes the event to disk and to the in-memory ring. Best-effort
// disk write — if it fails we still keep the in-memory entry so the SPA
// at least sees recent activity.
func (l *PersistentLog) Append(e Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ring = append(l.ring, e)
	if len(l.ring) > l.max {
		// Rewrite file with most-recent half once we exceed the cap.
		// Cheaper than truncating in-place and tolerates abrupt shutdown.
		keep := l.ring[len(l.ring)-l.max/2:]
		l.ring = append([]Event{}, keep...)
		l.rewriteLocked()
		return
	}
	// Normal path: append a single line.
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	_ = enc.Encode(e)
}

func (l *PersistentLog) rewriteLocked() {
	tmp := l.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, e := range l.ring {
		_ = enc.Encode(e)
	}
	_ = w.Flush()
	_ = f.Close()
	_ = os.Rename(tmp, l.path)
}

// List returns events in reverse chronological order (most recent first),
// optionally filtered by cluster and kind. Limit caps the result; 0
// means default (200). Cluster/kind empty match all.
func (l *PersistentLog) List(cluster string, kind Kind, limit int) []Event {
	if limit <= 0 {
		limit = 200
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	// Iterate newest-first.
	out := make([]Event, 0, limit)
	for i := len(l.ring) - 1; i >= 0 && len(out) < limit; i-- {
		e := l.ring[i]
		if cluster != "" && e.Cluster != cluster {
			continue
		}
		if kind != "" && e.Kind != kind {
			continue
		}
		out = append(out, e)
	}
	// Already newest-first; sort defensively in case ring contains out-
	// of-order entries from a clock skew.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

// PersistSync hooks an event subscription up to the persistent log,
// blocking until ctx is cancelled. The manager broadcasts on its own
// channels; we just relay each one to disk.
func (m *Manager) PersistSync(ctx context.Context, log *PersistentLog) {
	if log == nil {
		return
	}
	ch := m.Subscribe()
	defer m.Unsubscribe(ch)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			log.Append(ev)
		}
	}
}

// DrainTo is a helper used by tests / drain-on-shutdown to copy the
// current ring contents to a writer.
func (l *PersistentLog) DrainTo(w io.Writer) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	enc := json.NewEncoder(w)
	for _, e := range l.ring {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}

// approxAge is a tiny helper that says "in this many minutes" — used by
// the SPA via the Time field, not exposed in the API directly.
func approxAge(t time.Time) time.Duration { return time.Since(t) }
