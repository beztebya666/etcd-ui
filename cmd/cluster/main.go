// cluster — owns auto-discovery, the etcd connection pool and answers
// "what clusters do we know about" + "how is cluster X doing".
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/yourorg/etcd-ui/internal/alerts"
	"github.com/yourorg/etcd-ui/internal/config"
	"github.com/yourorg/etcd-ui/internal/discovery"
	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/httpx"
	"github.com/yourorg/etcd-ui/internal/logger"
	"github.com/yourorg/etcd-ui/internal/models"
	"github.com/yourorg/etcd-ui/internal/persist"
	"github.com/yourorg/etcd-ui/internal/tracing"

	"go.uber.org/zap"
)

// Version is set at build time via -ldflags.
var Version = "dev"

func main() {
	log := logger.New("cluster")
	cfg := config.Load()
	pool := etcdpool.New(cfg.DialTimeout)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	traceShutdown, err := tracing.Init(ctx, "cluster", Version)
	if err != nil {
		log.Warn("tracing init failed", zap.Error(err))
	}
	defer traceShutdown(context.Background())

	pool.WatchCerts(ctx)

	// Persistence — UI-added clusters survive restarts.
	store, err := persist.New(cfg.DataDir)
	if err != nil {
		log.Warn("persist init failed; UI clusters won't be saved", zap.Error(err))
	} else {
		if saved, err := store.Load(); err == nil {
			for _, c := range saved {
				if err := pool.Upsert(c); err != nil {
					log.Warn("rehydrate cluster", zap.String("id", c.ID), zap.Error(err))
				}
			}
			log.Info("rehydrated clusters from disk", zap.Int("count", len(saved)))
		}
	}
	savePersisted := func() {
		if store == nil {
			return
		}
		var manual []config.Cluster
		for _, c := range pool.List() {
			if c.Source == "manual" {
				manual = append(manual, c)
			}
		}
		if err := store.Save(manual); err != nil {
			log.Warn("persist save failed", zap.Error(err))
		}
	}

	sources := []discovery.Source{
		&discovery.EnvSource{Clusters: cfg.Clusters},
		discovery.NewFileSource(), // CLUSTERS_FILE=...
		discovery.NewDNSSource(),  // ETCD_UI_DNS_SRV=...
	}
	if len(cfg.PatroniURLs) > 0 {
		sources = append(sources, discovery.NewPatroniSource(cfg.PatroniURLs))
	}
	if cfg.KubernetesDiscovery {
		sources = append(sources, discovery.NewKubernetesSource())
	}
	mgr := discovery.NewManager(log, sources)

	// Run discovery in the background; first run is synchronous so the pool
	// has known endpoints by the time we start serving requests.
	mgr.RunOnce(ctx)
	for _, c := range mgr.Flatten() {
		if err := pool.Upsert(c); err != nil {
			log.Warn("initial dial failed", zap.String("cluster", c.ID), zap.Error(err))
		}
	}
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				mgr.RunOnce(ctx)
				for _, c := range mgr.Flatten() {
					_ = pool.Upsert(c)
				}
			}
		}
	}()

	// Health-change alerts: webhooks + SSE subscribers. Both opt-in via env.
	alertMgr := alerts.New(log)
	// Persist every emitted event to $DATA_DIR/cluster-events.jsonl so the
	// Metrics page can show a timeline that survives SPA reloads and
	// pod restarts. Best-effort: a failure here doesn't block alerting.
	eventLog, plErr := alerts.NewPersistentLog(cfg.DataDir)
	if plErr != nil {
		log.Warn("cluster event log disabled", zap.Error(plErr))
	} else {
		go alertMgr.PersistSync(ctx, eventLog)
	}
	go alertMgr.Watch(ctx, func() []models.ClusterSummary {
		out := make([]models.ClusterSummary, 0, len(pool.List()))
		for _, c := range pool.List() {
			sctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			s, _ := pool.Summary(sctx, c.ID)
			cancel()
			out = append(out, s)
		}
		return out
	}, 15*time.Second)

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer, middleware.StripSlashes, httpx.Logging(log))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Historical alert log — leader flips / health flips / alarm onsets
	// since the cluster service first started. Lets the Metrics page
	// answer "WHEN did the 5 leader changes happen?" without requiring
	// a Prometheus TSDB. ?cluster=&kind=&limit= filters.
	r.Get("/alerts/log", func(w http.ResponseWriter, r *http.Request) {
		if eventLog == nil {
			httpx.JSON(w, 200, []alerts.Event{})
			return
		}
		clusterID := r.URL.Query().Get("cluster")
		kind := alerts.Kind(r.URL.Query().Get("kind"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		httpx.JSON(w, 200, eventLog.List(clusterID, kind, limit))
	})

	// Dual-transport stream of health alerts. WebSocket-first (clients can
	// upgrade), SSE fallback for older proxies / curl. SPA subscribes via
	// /lib/stream which prefers WS so the StreamStatus pill stays green.
	r.Get("/alerts/stream", func(w http.ResponseWriter, r *http.Request) {
		ch := alertMgr.Subscribe()
		defer alertMgr.Unsubscribe(ch)

		if ws, err := httpx.Upgrade(w, r); err == nil {
			defer ws.Close()
			tick := time.NewTicker(15 * time.Second)
			defer tick.Stop()
			for {
				select {
				case <-r.Context().Done():
					return
				case <-tick.C:
					if err := ws.Ping(); err != nil {
						return
					}
				case ev := <-ch:
					b, _ := json.Marshal(ev)
					if err := ws.SendText(b); err != nil {
						return
					}
				}
			}
		}

		flusher, err := httpx.SSE(w)
		if err != nil {
			httpx.Err(w, 500, err)
			return
		}
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
				_, _ = w.Write([]byte(": ping\n\n"))
				flusher.Flush()
			case ev := <-ch:
				b, _ := json.Marshal(ev)
				_, _ = w.Write([]byte("data: "))
				_, _ = w.Write(b)
				_, _ = w.Write([]byte("\n\n"))
				flusher.Flush()
			}
		}
	})

	// Public-facing: same data without secrets.
	r.Get("/clusters", func(w http.ResponseWriter, r *http.Request) {
		var out []models.ClusterSummary
		for _, c := range pool.List() {
			s, _ := pool.Summary(r.Context(), c.ID)
			out = append(out, s)
		}
		httpx.JSON(w, 200, out)
	})

	r.Get("/clusters/{id}/summary", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		s, err := pool.Summary(r.Context(), id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		httpx.JSON(w, 200, s)
	})

	r.Get("/clusters/{id}/members", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		cli, c, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		mctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		ml, err := cli.MemberList(mctx)
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}
		var leader uint64
		for _, ep := range c.Endpoints {
			if st, e := cli.Status(mctx, ep); e == nil {
				leader = st.Leader
				break
			}
		}
		out := make([]models.Member, 0, len(ml.Members))
		for _, m := range ml.Members {
			out = append(out, models.Member{
				ID:         m.ID,
				IDStr:      strconv.FormatUint(m.ID, 10),
				Name:       m.Name,
				PeerURLs:   m.PeerURLs,
				ClientURLs: m.ClientURLs,
				IsLeader:   m.ID == leader,
				IsLearner:  m.IsLearner,
			})
		}
		httpx.JSON(w, 200, out)
	})

	// Manual add/remove (used by the gateway when a user creates a cluster in UI).
	r.Post("/clusters", func(w http.ResponseWriter, r *http.Request) {
		var c config.Cluster
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		if c.Source == "" {
			c.Source = "manual"
		}
		if err := pool.Upsert(c); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		savePersisted()
		httpx.JSON(w, 201, c)
	})
	r.Delete("/clusters/{id}", func(w http.ResponseWriter, r *http.Request) {
		pool.Remove(chi.URLParam(r, "id"))
		savePersisted()
		w.WriteHeader(204)
	})

	// Internal-only: peers (kv, ops) consume this to sync their own pools.
	r.Get("/internal/clusters", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, 200, pool.List())
	})

	srv := &http.Server{Addr: cfg.ClusterAddr, Handler: tracing.WrapHandler("etcd-ui.cluster", r)}
	go func() {
		log.Info("cluster service listening", zap.String("addr", cfg.ClusterAddr))
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
