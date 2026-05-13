// kv — Range/Put/Delete + watch SSE streams. Connection pool is kept in sync
// with the cluster service via internal/peer.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/yourorg/etcd-ui/internal/config"
	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/httpx"
	"github.com/yourorg/etcd-ui/internal/logger"
	"github.com/yourorg/etcd-ui/internal/models"
	"github.com/yourorg/etcd-ui/internal/peer"
	"github.com/yourorg/etcd-ui/internal/tracing"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

// Version is set at build time via -ldflags.
var Version = "dev"

func main() {
	log := logger.New("kv")
	cfg := config.Load()
	pool := etcdpool.New(cfg.DialTimeout)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	traceShutdown, terr := tracing.Init(ctx, "kv", Version)
	if terr != nil {
		log.Warn("tracing init failed", zap.Error(terr))
	}
	defer traceShutdown(context.Background())

	// Dial env-configured clusters immediately so we can serve the very first
	// request, then rely on peer-sync to pick up anything added at runtime.
	for _, c := range cfg.Clusters {
		if err := pool.Upsert(c); err != nil {
			log.Warn("startup dial", zap.String("cluster", c.ID), zap.Error(err))
		}
	}
	s := peer.NewSyncer("http://"+cfg.ClusterAddr, pool, log)
	go s.Run(ctx)

	guard := httpx.NewReadonlyGuard(cfg.ReadonlyClusters)
	s.SetReadonlyApplier(guard)

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer, middleware.StripSlashes, httpx.Logging(log))
	r.Use(guard.Middleware)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })

	r.Group(bulkHandlers(pool))
	r.Get("/clusters/{id}/history", historyHandler(pool))
	r.Get("/clusters/{id}/diff", diffHandler(pool))

	r.Post("/clusters/{id}/range", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req models.RangeRequest
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		if pool.APIVersion(id) == etcdpool.APIv2 {
			v2RangeHandler(w, r, pool, id, req)
			return
		}
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		opts := []clientv3.OpOption{}
		key := req.From
		switch {
		case req.Prefix != "":
			key = req.Prefix
			opts = append(opts, clientv3.WithPrefix())
		case req.End != "":
			opts = append(opts, clientv3.WithRange(req.End))
		case req.From == "":
			// range over everything
			key = "\x00"
			opts = append(opts, clientv3.WithFromKey())
		}
		if req.Limit > 0 {
			opts = append(opts, clientv3.WithLimit(req.Limit))
		}
		if req.KeysOnly {
			opts = append(opts, clientv3.WithKeysOnly())
		}
		if req.CountOnly {
			opts = append(opts, clientv3.WithCountOnly())
		}
		if req.Revision > 0 {
			opts = append(opts, clientv3.WithRev(req.Revision))
		}
		opts = append(opts, clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend))

		gctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		gctx, endSpan := tracing.EtcdSpan(gctx, "range", id)
		resp, err := cli.Get(gctx, key, opts...)
		endSpan(err)
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

		out := models.RangeResponse{More: resp.More}
		out.KVs = make([]models.KV, 0, len(resp.Kvs))
		for _, k := range resp.Kvs {
			if keyRe != nil && !keyRe.Match(k.Key) {
				continue
			}
			if valRe != nil && !valRe.Match(k.Value) {
				continue
			}
			out.KVs = append(out.KVs, models.KV{
				Key:            string(k.Key),
				Value:          string(k.Value),
				CreateRevision: k.CreateRevision,
				ModRevision:    k.ModRevision,
				Version:        k.Version,
				Lease:          k.Lease,
				Preview:        kvPreview(k.Value),
			})
		}
		out.Count = int64(len(out.KVs))
		httpx.JSON(w, 200, out)
	})

	// /range/counts — returns per-subfolder key counts under `prefix` at
	// `depth`. Cheap (etcd serves Count from MVCC without scanning) but
	// O(N) sub-queries, so we cap at 200 top-level buckets. SPA calls
	// this only when a Range hit the limit, to mark "▸ pods (12.3k)" on
	// folders the tree didn't fully load.
	r.Post("/clusters/{id}/range/counts", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if pool.APIVersion(id) == etcdpool.APIv2 {
			httpx.Err(w, 400, errString("folder counts require etcd v3"))
			return
		}
		var req struct {
			Prefix string `json:"prefix"`
			Depth  int    `json:"depth"`
		}
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		if req.Depth <= 0 {
			req.Depth = 2
		}
		if req.Depth > 4 {
			req.Depth = 4
		}
		prefix := req.Prefix
		if prefix == "" || prefix == "/" {
			prefix = "/"
		}
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		// First: get key list keysOnly under the prefix (no values, cheap)
		// at a generous cap to discover what subfolders exist. For 27k
		// keys this is a single batched scan, much faster than enumerating
		// at any depth client-side.
		resp, err := cli.Get(ctx, prefix,
			clientv3.WithPrefix(),
			clientv3.WithKeysOnly(),
			clientv3.WithLimit(0),
		)
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}
		// Aggregate keys into buckets at the requested depth, e.g.
		// /registry/pods/default/foo → bucket "/registry/pods" at depth 2.
		buckets := map[string]int64{}
		for _, kv := range resp.Kvs {
			k := string(kv.Key)
			// strip leading slash for splitting
			parts := strings.SplitN(strings.TrimPrefix(k, "/"), "/", req.Depth+1)
			if len(parts) < req.Depth {
				continue
			}
			bkt := "/" + strings.Join(parts[:req.Depth], "/")
			buckets[bkt]++
		}
		// Cap response size; sort by count descending for the UI.
		type entry struct {
			Prefix string `json:"prefix"`
			Count  int64  `json:"count"`
		}
		out := make([]entry, 0, len(buckets))
		for k, c := range buckets {
			out = append(out, entry{Prefix: k, Count: c})
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
		if len(out) > 200 {
			out = out[:200]
		}
		httpx.JSON(w, 200, map[string]any{
			"prefix":  prefix,
			"depth":   req.Depth,
			"total":   resp.Count,
			"buckets": out,
		})
	})

	r.Post("/clusters/{id}/put", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req models.PutRequest
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		if pool.APIVersion(id) == etcdpool.APIv2 {
			v2PutHandler(w, r, pool, id, req)
			return
		}
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		opts := []clientv3.OpOption{}
		if req.LeaseID != 0 {
			opts = append(opts, clientv3.WithLease(clientv3.LeaseID(req.LeaseID)))
		}
		pctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		pctx, endSpan := tracing.EtcdSpan(pctx, "put", id)
		resp, err := cli.Put(pctx, req.Key, req.Value, opts...)
		endSpan(err)
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}
		httpx.JSON(w, 200, map[string]any{"revision": resp.Header.Revision})
	})

	// Compare-and-set put with 3-way merge fallback. The SPA editor uses
	// this instead of /put when the user is editing an existing key — if
	// somebody else wrote the same key while we were typing, the server
	// runs a line-based 3-way merge before either committing the result
	// or returning a conflict for the SPA to render.
	r.Post("/clusters/{id}/put-cas", casHandler(pool))

	r.Post("/clusters/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req models.DeleteRequest
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		if pool.APIVersion(id) == etcdpool.APIv2 {
			v2DeleteHandler(w, r, pool, id, req)
			return
		}
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}

		dctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// Capture previous value for audit-undo (single keys only).
		if !req.Prefix {
			if g, err := cli.Get(dctx, req.Key); err == nil && len(g.Kvs) > 0 {
				v := string(g.Kvs[0].Value)
				if len(v) > 4096 {
					v = v[:4096]
				}
				w.Header().Set("X-Etcd-UI-Key", req.Key)
				w.Header().Set("X-Etcd-UI-Prev-Value", base64Std(v))
			}
		}

		opts := []clientv3.OpOption{}
		if req.Prefix {
			opts = append(opts, clientv3.WithPrefix())
		}
		dctx, endSpan := tracing.EtcdSpan(dctx, "delete", id)
		resp, err := cli.Delete(dctx, req.Key, opts...)
		endSpan(err)
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}
		httpx.JSON(w, 200, map[string]any{
			"revision": resp.Header.Revision,
			"deleted":  resp.Deleted,
		})
	})

	// Watch is dual-transport: clients that send `Upgrade: websocket` get a
	// WS stream; everyone else gets the legacy SSE stream. The payload shape
	// is identical so the SPA can prefer WS and fall back transparently.
	r.Get("/clusters/{id}/watch", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if pool.APIVersion(id) == etcdpool.APIv2 {
			v2WatchHandler(w, r, pool, id)
			return
		}
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		if ws, err := httpx.Upgrade(w, r); err == nil {
			wsWatch(ws, cli, r)
			return
		}
		key := r.URL.Query().Get("prefix")
		opts := []clientv3.OpOption{clientv3.WithPrefix(), clientv3.WithPrevKV()}
		if rev := r.URL.Query().Get("rev"); rev != "" {
			n, _ := strconv.ParseInt(rev, 10, 64)
			if n > 0 {
				opts = append(opts, clientv3.WithRev(n))
			}
		}

		flusher, err := httpx.SSE(w)
		if err != nil {
			httpx.Err(w, 500, err)
			return
		}

		wctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		ch := cli.Watch(wctx, key, opts...)
		// keep-alive ticker so proxies don't kill an idle connection
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
				_, _ = fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			case wr, ok := <-ch:
				if !ok {
					return
				}
				if err := wr.Err(); err != nil {
					_, _ = fmt.Fprintf(w, "event: error\ndata: %q\n\n", err.Error())
					flusher.Flush()
					return
				}
				for _, ev := range wr.Events {
					out := models.WatchEvent{
						Type:     ev.Type.String(),
						Key:      string(ev.Kv.Key),
						Value:    string(ev.Kv.Value),
						Revision: ev.Kv.ModRevision,
						Preview:  kvPreview(ev.Kv.Value),
					}
					if ev.PrevKv != nil {
						out.PrevValue = string(ev.PrevKv.Value)
					}
					b, _ := json.Marshal(out)
					_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
				}
				flusher.Flush()
			}
		}
	})

	srv := &http.Server{Addr: cfg.KVAddr, Handler: tracing.WrapHandler("etcd-ui.kv", r)}
	go func() {
		log.Info("kv service listening", zap.String("addr", cfg.KVAddr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("listen", zap.Error(err))
		}
	}()
	<-ctx.Done()
	sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(sh)
	pool.Close()
}

