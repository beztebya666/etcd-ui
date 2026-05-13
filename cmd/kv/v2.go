package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/httpx"
	"github.com/yourorg/etcd-ui/internal/models"
)

// v2 handler shims — entered from main.go whenever pool.APIVersion(id) == APIv2.
// Each one accepts the already-decoded request body to keep the call sites in
// main.go small.

func v2Client(pool *etcdpool.Pool, id string) (*etcdpool.V2Client, error) {
	cli := pool.V2(id)
	if cli == nil {
		return nil, fmt.Errorf("cluster %q not connected (v2)", id)
	}
	return cli, nil
}

func v2RangeHandler(w http.ResponseWriter, r *http.Request, pool *etcdpool.Pool, id string, req models.RangeRequest) {
	cli, err := v2Client(pool, id)
	if err != nil {
		httpx.Err(w, 404, err)
		return
	}
	key, withPrefix := v2RangeArgs(req)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	kvs, err := cli.Range(ctx, key, withPrefix, req.Limit)
	if err != nil {
		httpx.Err(w, 502, err)
		return
	}

	var keyRe, valRe *regexp.Regexp
	if req.KeyRegex != "" {
		keyRe, err = regexp.Compile(req.KeyRegex)
		if err != nil {
			httpx.Err(w, 400, fmt.Errorf("keyRegex: %w", err))
			return
		}
	}
	if req.ValueRegex != "" {
		valRe, err = regexp.Compile(req.ValueRegex)
		if err != nil {
			httpx.Err(w, 400, fmt.Errorf("valueRegex: %w", err))
			return
		}
	}

	out := models.RangeResponse{KVs: make([]models.KV, 0, len(kvs))}
	for _, k := range kvs {
		if keyRe != nil && !keyRe.MatchString(k.Key) {
			continue
		}
		if valRe != nil && !valRe.MatchString(k.Value) {
			continue
		}
		if req.KeysOnly {
			k.Value = ""
		}
		out.KVs = append(out.KVs, k)
	}
	out.Count = int64(len(out.KVs))
	httpx.JSON(w, 200, out)
}

func v2RangeArgs(req models.RangeRequest) (string, bool) {
	switch {
	case req.Prefix != "":
		return req.Prefix, true
	case req.From != "":
		// v2 has no /range — closest analog is "recursive get from this key".
		return req.From, true
	default:
		return "/", true
	}
}

func v2PutHandler(w http.ResponseWriter, r *http.Request, pool *etcdpool.Pool, id string, req models.PutRequest) {
	cli, err := v2Client(pool, id)
	if err != nil {
		httpx.Err(w, 404, err)
		return
	}
	if req.LeaseID != 0 {
		httpx.Err(w, 501, errors.New("etcd v2 does not support leases (use TTLs on individual keys via etcdctl set --ttl)"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := cli.Set(ctx, req.Key, req.Value)
	if err != nil {
		httpx.Err(w, 502, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"revision": int64(resp.Index)})
}

func v2DeleteHandler(w http.ResponseWriter, r *http.Request, pool *etcdpool.Pool, id string, req models.DeleteRequest) {
	cli, err := v2Client(pool, id)
	if err != nil {
		httpx.Err(w, 404, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Capture previous value for audit-undo on single-key deletes.
	if !req.Prefix {
		if g, err := cli.Get(ctx, req.Key, false); err == nil && g.Node != nil && !g.Node.Dir {
			v := g.Node.Value
			if len(v) > 4096 {
				v = v[:4096]
			}
			w.Header().Set("X-Etcd-UI-Key", req.Key)
			w.Header().Set("X-Etcd-UI-Prev-Value", base64Std(v))
		}
	}
	resp, err := cli.Delete(ctx, req.Key, req.Prefix)
	if err != nil {
		httpx.Err(w, 502, err)
		return
	}
	deleted := int64(1)
	if req.Prefix {
		deleted = 0 // v2 doesn't report deleted-count
	}
	httpx.JSON(w, 200, map[string]any{
		"revision": int64(resp.Index),
		"deleted":  deleted,
	})
}

func v2WatchHandler(w http.ResponseWriter, r *http.Request, pool *etcdpool.Pool, id string) {
	cli, err := v2Client(pool, id)
	if err != nil {
		httpx.Err(w, 404, err)
		return
	}
	key := r.URL.Query().Get("prefix")
	if key == "" {
		key = "/"
	}

	flusher, err := httpx.SSE(w)
	if err != nil {
		httpx.Err(w, 500, err)
		return
	}

	wctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	ch := make(chan models.WatchEvent, 32)
	go func() { _ = cli.Watch(wctx, key, ch) }()

	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(ev)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
	}
}

// v2NotSupported is a uniform 501 response for handlers that have no v2 analog
// (txn, history walk, transactional bulk). Used by bulk.go / history.go to
// short-circuit before they reach into the (nil) v3 client.
func v2NotSupported(w http.ResponseWriter, feature string) {
	httpx.Err(w, 501, fmt.Errorf("%s is unavailable on etcd v2 clusters — v2 lacks MVCC/txn/leases", feature))
}

// v2Export streams every key under "/" as the same shape /export returns for v3.
func v2Export(w http.ResponseWriter, r *http.Request, pool *etcdpool.Pool, id, name string) {
	cli, err := v2Client(pool, id)
	if err != nil {
		httpx.Err(w, 404, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	kvs, err := cli.Range(ctx, "/", true, 0)
	if err != nil {
		httpx.Err(w, 502, err)
		return
	}
	out := models.RestoreSnapshot{
		Cluster:    name,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		KVs:        kvs,
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition",
		`attachment; filename="etcd-`+strings.ReplaceAll(name, " ", "-")+`-v2-`+time.Now().UTC().Format("20060102T150405Z")+`.json"`)
	httpx.JSON(w, 200, out)
}

// v2BulkPut loops Set calls (non-transactional — v2 has no txn).
func v2BulkPut(w http.ResponseWriter, r *http.Request, pool *etcdpool.Pool, id string, req models.BulkPutRequest) {
	if req.Transactional {
		v2NotSupported(w, "transactional bulk-put")
		return
	}
	cli, err := v2Client(pool, id)
	if err != nil {
		httpx.Err(w, 404, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out := models.BulkResult{}
	for _, it := range req.Items {
		if it.LeaseID != 0 {
			out.Failed++
			out.Error = "leases unavailable on v2"
			continue
		}
		resp, err := cli.Set(ctx, it.Key, it.Value)
		if err != nil {
			out.Failed++
			out.Error = err.Error()
			continue
		}
		out.Applied++
		out.Revision = int64(resp.Index)
	}
	httpx.JSON(w, 200, out)
}

func v2BulkDelete(w http.ResponseWriter, r *http.Request, pool *etcdpool.Pool, id string, req models.BulkDeleteRequest) {
	cli, err := v2Client(pool, id)
	if err != nil {
		httpx.Err(w, 404, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Audit-undo prev-value capture, same budget as the v3 path (32 keys
	// × 4KB). v2 has no WithPrevKV — we issue a serial Get-then-Delete
	// per key. Slower but matches v3 audit semantics exactly.
	const maxBulkPrev = 32
	const maxValueBytes = 4096
	type prevKV struct {
		Key string `json:"k"`
		Val string `json:"v"`
	}
	prevs := make([]prevKV, 0, maxBulkPrev)

	var revision int64
	deleted := int64(0)
	for _, k := range req.Keys {
		if len(prevs) < maxBulkPrev {
			if g, gErr := cli.Get(ctx, k, false); gErr == nil && g.Node != nil && !g.Node.Dir {
				v := g.Node.Value
				if len(v) > maxValueBytes {
					v = v[:maxValueBytes]
				}
				prevs = append(prevs, prevKV{Key: k, Val: base64Std(v)})
			}
		}
		resp, err := cli.Delete(ctx, k, false)
		if err != nil {
			continue
		}
		revision = int64(resp.Index)
		deleted++
	}
	for _, p := range req.Prefixes {
		// Prefix delete on v2: Get-recursive to collect prev-values up to
		// the cap, then a single recursive Delete. cli.Range flattens the
		// tree for us so the math is straightforward.
		if len(prevs) < maxBulkPrev {
			if kvs, err := cli.Range(ctx, p, true, 0); err == nil {
				for _, kv := range kvs {
					if len(prevs) >= maxBulkPrev {
						break
					}
					v := kv.Value
					if len(v) > maxValueBytes {
						v = v[:maxValueBytes]
					}
					prevs = append(prevs, prevKV{Key: kv.Key, Val: base64Std(v)})
				}
			}
		}
		resp, err := cli.Delete(ctx, p, true)
		if err != nil {
			continue
		}
		revision = int64(resp.Index)
		// v2 doesn't report a deleted-count; gauge stays at 0.
	}

	if len(prevs) > 0 {
		if b, err := json.Marshal(prevs); err == nil {
			w.Header().Set("X-Etcd-UI-Bulk-Prev", base64Std(string(b)))
		}
	}
	httpx.JSON(w, 200, map[string]any{
		"deleted":  deleted,
		"revision": revision,
	})
}
