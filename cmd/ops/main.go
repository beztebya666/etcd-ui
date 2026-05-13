// ops — snapshots, leases, compaction, defrag, alarms.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	clientv3 "go.etcd.io/etcd/client/v3"
	"github.com/yourorg/etcd-ui/internal/config"
	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/httpx"
	"github.com/yourorg/etcd-ui/internal/logger"
	"github.com/yourorg/etcd-ui/internal/models"
	"github.com/yourorg/etcd-ui/internal/peer"
	"github.com/yourorg/etcd-ui/internal/tracing"

	"go.uber.org/zap"
)

// Version is set at build time via -ldflags.
var Version = "dev"

func main() {
	log := logger.New("ops")
	cfg := config.Load()
	pool := etcdpool.New(cfg.DialTimeout)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	traceShutdown, terr := tracing.Init(ctx, "ops", Version)
	if terr != nil {
		log.Warn("tracing init failed", zap.Error(terr))
	}
	defer traceShutdown(context.Background())

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

	r.Group(rbacHandlers(pool))
	r.Post("/clusters/{id}/restore", restoreHandler(pool))
	r.Get("/clusters/{id}/restore/recipe", restoreRecipeHandler(pool))
	r.Get("/clusters/{id}/metrics", metricsHandler(pool))
	r.Get("/clusters/{id}/snapshots", snapshotsListHandler(cfg.DataDir))
	r.Get("/clusters/{id}/snapshots/{name}", snapshotsDownloadHandler(cfg.DataDir))
	r.Post("/clusters/{id}/etcdctl", etcdctlHandler(pool))
	// etcdutl is cluster-agnostic (it operates on uploaded snapshot files),
	// but we keep it under /clusters/{id}/ for permission consistency with
	// the rest of ops — admin on the cluster gates "validate any snapshot
	// blob the server can read".
	r.Post("/clusters/{id}/etcdutl/snapshot-status", etcdutlStatusHandler())

	// Transfer raft leadership to a specific member. See cmd/ops/move_leader.go
	// for why this lives outside the etcdctl gate.
	r.Post("/clusters/{id}/move-leader", moveLeaderHandler(pool))

	// Distributed-lock playground — see cmd/ops/locks.go for the model.
	r.Get("/clusters/{id}/locks", locksListHandler(pool))
	r.Post("/clusters/{id}/locks/acquire", lockAcquireHandler(pool))
	r.Delete("/clusters/{id}/locks/{leaseId}", lockReleaseHandler(pool))

	startScheduler(ctx, log, pool, cfg.DataDir)

	// Snapshot — streams etcd snapshot bytes back to the caller.
	r.Get("/clusters/{id}/snapshot", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		cli, c, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		sctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		rc, err := cli.Snapshot(sctx)
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}
		defer rc.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`attachment; filename="etcd-%s-%s.db"`, c.Name, time.Now().UTC().Format("20060102T150405Z")))
		if _, err := io.Copy(w, rc); err != nil {
			log.Warn("snapshot copy", zap.Error(err))
		}
	})

	r.Get("/clusters/{id}/leases", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		lctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		leases, err := cli.Leases(lctx)
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}
		// Cap how many leases we enrich per page load. Enriching every
		// lease on a 5000-node cluster would be ~5000 extra Get calls.
		// 200 is plenty for the table viewport and matches what the SPA
		// shows above the fold.
		const enrichCap = 200
		out := make([]models.Lease, 0, len(leases.Leases))
		for _, l := range leases.Leases {
			info, err := cli.TimeToLive(lctx, l.ID, clientv3.WithAttachedKeys())
			if err != nil {
				continue
			}
			lm := models.Lease{
				ID:            int64(info.ID),
				TTL:           info.TTL,
				GrantedTTL:    info.GrantedTTL,
				AttachedCount: len(info.Keys),
			}
			// Trim the attached-key list we send to the SPA — Vault and
			// some controllers attach thousands of keys to a single lease.
			const sendCap = 20
			for i, k := range info.Keys {
				if i >= sendCap {
					break
				}
				lm.AttachedKeys = append(lm.AttachedKeys, string(k))
			}
			if len(out) < enrichCap && len(info.Keys) > 0 {
				en := enrichLease(lctx, cli, lm.AttachedKeys)
				lm.HolderIdentity = en.HolderIdentity
				lm.HolderKind = en.HolderKind
				lm.RenewedAt = en.RenewedAt
				lm.AcquiredAt = en.AcquiredAt
				lm.LeaseTransitions = en.LeaseTransitions
			}
			out = append(out, lm)
		}
		httpx.JSON(w, 200, out)
	})

	r.Delete("/clusters/{id}/leases/{leaseId}", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		n, _ := strconv.ParseInt(chi.URLParam(r, "leaseId"), 10, 64)
		lctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if _, err := cli.Revoke(lctx, asLeaseID(n)); err != nil {
			httpx.Err(w, 502, err)
			return
		}
		w.WriteHeader(204)
	})

	r.Post("/clusters/{id}/compact", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		rev, _ := strconv.ParseInt(r.URL.Query().Get("rev"), 10, 64)
		if rev <= 0 {
			cctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			if st, err := cli.Status(cctx, currentEndpoint(cli)); err == nil {
				rev = st.Header.Revision
			}
		}
		cctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if _, err := cli.Compact(cctx, rev); err != nil {
			httpx.Err(w, 502, err)
			return
		}
		httpx.JSON(w, 200, map[string]any{"compactedRevision": rev})
	})

	r.Post("/clusters/{id}/defrag", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		cli, c, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		out := map[string]string{}
		for _, ep := range c.Endpoints {
			ctxd, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			_, err := cli.Defragment(ctxd, ep)
			cancel()
			if err != nil {
				out[ep] = "error: " + err.Error()
			} else {
				out[ep] = "ok"
			}
		}
		httpx.JSON(w, 200, out)
	})

	r.Post("/clusters/{id}/alarms/disarm", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		actx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		_, err = cli.AlarmDisarm(actx, nil)
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}
		httpx.JSON(w, 200, map[string]any{"disarmed": true})
	})

	srv := &http.Server{Addr: cfg.OpsAddr, Handler: tracing.WrapHandler("etcd-ui.ops", r)}
	go func() {
		log.Info("ops service listening", zap.String("addr", cfg.OpsAddr))
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
