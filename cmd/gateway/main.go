// gateway — only externally exposed process. It serves the SPA from a
// filesystem root and proxies /api/* to the right internal service:
//
//	cluster: /api/clusters, /api/clusters/{id}, /api/clusters/{id}/{summary,members}
//	kv:      /api/clusters/{id}/{range,put,delete,watch,bulk/*,txn,export}
//	ops:     /api/clusters/{id}/{snapshot,restore,leases,leases/*,compact,defrag,alarms/*,rbac/*}
//	audit:   /api/audit/*
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/http/pprof"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/yourorg/etcd-ui/internal/auth"
	"github.com/yourorg/etcd-ui/internal/config"
	"github.com/yourorg/etcd-ui/internal/federation"
	"github.com/yourorg/etcd-ui/internal/httpx"
	"github.com/yourorg/etcd-ui/internal/logger"
	"github.com/yourorg/etcd-ui/internal/tracing"

	"go.uber.org/zap"
)

// Version is set at build time via -ldflags="-X main.Version=$(git rev-parse --short HEAD)".
var Version = "dev"

// exeModTime returns the modification time of our own binary, used as a
// last-resort version hint when -ldflags wasn't passed.
func exeModTime() time.Time {
	p, err := os.Executable()
	if err != nil {
		return time.Time{}
	}
	st, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

func main() {
	log := logger.New("gateway")
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	traceShutdown, err := tracing.Init(ctx, "gateway", Version)
	if err != nil {
		log.Warn("tracing init failed", zap.Error(err))
	}
	metricsShutdown, merr := tracing.InitMetrics(ctx, "gateway", Version)
	if merr != nil {
		log.Warn("otel metrics init failed", zap.Error(merr))
	}
	defer metricsShutdown(context.Background())
	defer traceShutdown(context.Background())

	clusterURL, _ := url.Parse("http://" + cfg.ClusterAddr)
	kvURL, _ := url.Parse("http://" + cfg.KVAddr)
	opsURL, _ := url.Parse("http://" + cfg.OpsAddr)
	auditURL, _ := url.Parse("http://" + cfg.AuditAddr)

	clusterProxy := newProxy(clusterURL)
	kvProxy := newProxy(kvURL)
	opsProxy := newProxy(opsURL)
	auditProxy := newProxy(auditURL)

	metrics := httpx.NewMetrics()
	if err := metrics.RegisterOTel("etcd-ui/gateway"); err != nil {
		log.Warn("otel metrics register failed", zap.Error(err))
	}
	readiness := httpx.NewReadinessProbe([]string{
		"http://" + cfg.ClusterAddr,
		"http://" + cfg.KVAddr,
		"http://" + cfg.OpsAddr,
		"http://" + cfg.AuditAddr,
	})
	readiness.Start(ctx)

	rateRPS := envInt("ETCD_UI_RATE_RPS", 30)
	rateBurst := envInt("ETCD_UI_RATE_BURST", 60)
	limiter := httpx.NewRateLimiter(rateRPS, rateBurst)

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer, httpx.Logging(log))
	r.Use(httpx.SecurityHeaders)
	r.Use(metrics.Middleware)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: httpx.AllowedOrigins(),
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"*"},
		AllowCredentials: true,
	}))
	a := auth.New()
	if a.Enabled() {
		log.Info("HTTP Basic auth enabled")
	}
	r.Use(a.Middleware)
	acl := httpx.NewACL()
	if err := acl.LoadFromEnv(); err != nil {
		log.Warn("ACL load failed; per-cluster RBAC disabled", zap.Error(err))
	} else if acl.Loaded() {
		log.Info("per-cluster RBAC loaded", zap.Int("rules", len(acl.Rules())))
	}
	acl.OnReload(func(n int) {
		if n < 0 {
			log.Warn("ACL hot-reload failed — keeping previous rules")
			return
		}
		log.Info("ACL hot-reloaded", zap.Int("rules", n))
	})
	// Bootstrap admin. If ETCD_UI_ACL_BOOTSTRAP_ADMIN names a user and no
	// __acl__ admin rule exists yet, grant it on startup. Lets a fresh
	// deployment make this user a self-sufficient operator without manual
	// editing of ETCD_UI_ACL. Idempotent — re-running is a no-op.
	if u := os.Getenv("ETCD_UI_ACL_BOOTSTRAP_ADMIN"); u != "" {
		granted, err := acl.EnsureBootstrapAdmin(u)
		switch {
		case err != nil:
			log.Warn("ACL bootstrap admin failed", zap.String("user", u), zap.Error(err))
		case granted:
			log.Info("ACL bootstrap admin granted",
				zap.String("user", u),
				zap.String("cluster", "__acl__"))
		}
	}
	// First-login bootstrap: opt-in alternative for deployments that don't
	// know the admin's identity ahead of time. The first user to complete
	// OIDC verification gets `__acl__` admin — sync.Once-gated.
	if os.Getenv("ETCD_UI_ACL_BOOTSTRAP_FIRST_LOGIN") == "on" {
		a.OnFirstLogin(func(user string) {
			granted, err := acl.EnsureBootstrapAdmin(user)
			switch {
			case err != nil:
				log.Warn("ACL first-login bootstrap failed",
					zap.String("user", user), zap.Error(err))
			case granted:
				log.Info("ACL first-login bootstrap granted",
					zap.String("user", user))
			}
		})
	}
	// History dir lets the editor diff / restore prior snapshots.
	if dir := os.Getenv("ETCD_UI_DATA_DIR"); dir != "" {
		acl.SetHistoryDir(dir + "/acl-history")
	} else {
		acl.SetHistoryDir("/app/data/acl-history")
	}
	go acl.Watch(ctx, 0)
	r.Use(acl.Middleware)
	r.Use(limiter.Middleware)
	r.Use(auditEmitter(log, "http://"+cfg.AuditAddr, metrics))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/readyz", readiness.Handler())
	r.Get("/metrics", metrics.Handler())
	r.Post("/api/auth/login", a.LoginHandler())
	r.Post("/api/auth/logout", a.LogoutHandler())
	r.Get("/api/auth/me", a.MeHandler())
	// OIDC authorization-code + PKCE login flow. Routes are no-ops (404) when
	// ETCD_UI_OIDC_CLIENT_ID / REDIRECT_URL are not set.
	a.OAuth().Mount(r)
	// Federation hub. Disabled (returns 404) when no ETCD_UI_PEERS is set.
	fed := federation.NewFromEnv(log)
	if fed != nil {
		go fed.Watch(ctx, 0)
		r.Get("/api/federation/peers", func(w http.ResponseWriter, _ *http.Request) {
			httpx.JSON(w, 200, fed.Snapshot())
		})
		r.Get("/api/federation/clusters", func(w http.ResponseWriter, _ *http.Request) {
			httpx.JSON(w, 200, fed.AggregateClusters())
		})
	}

	r.Get("/api/acl", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, 200, map[string]any{
			"loaded":   acl.Loaded(),
			"rules":    acl.Rules(),
			"editable": acl.Path() != "",
		})
	})
	// Dry-run validation. SPA uses this to surface "your rule 7 has an
	// invalid access value" *before* showing the diff confirm dialog.
	r.Post("/api/acl/validate", func(w http.ResponseWriter, req *http.Request) {
		var rules []httpx.ACLRule
		if err := json.NewDecoder(req.Body).Decode(&rules); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		if err := acl.ValidateRules(rules); err != nil {
			httpx.JSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		httpx.JSON(w, 200, map[string]any{"ok": true, "rules": len(rules)})
	})
	// History list — newest first. Used by the "Undo last edit" UI path.
	r.Get("/api/acl/history", func(w http.ResponseWriter, req *http.Request) {
		user := req.Header.Get("X-Etcd-UI-User")
		if !aclCanEdit(acl, user) {
			httpx.Err(w, 403, errSimple("history requires admin on cluster __acl__"))
			return
		}
		hist, err := acl.ListHistory(50)
		if err != nil {
			httpx.Err(w, 500, err)
			return
		}
		// Strip server-side filesystem paths from the response — the SPA
		// only needs the timestamp + actor + rules to render.
		type item struct {
			When  string         `json:"when"`
			Actor string         `json:"actor"`
			ID    string         `json:"id"` // base name (used by /restore)
			Rules []httpx.ACLRule `json:"rules"`
		}
		out := make([]item, 0, len(hist))
		for _, h := range hist {
			out = append(out, item{
				When:  h.When.Format(time.RFC3339Nano),
				Actor: h.Actor,
				ID:    filepath.Base(h.File),
				Rules: h.Rules,
			})
		}
		httpx.JSON(w, 200, out)
	})
	// Undo: restore a specific historical snapshot. Body: {"id":"<filename>"}.
	r.Post("/api/acl/restore", func(w http.ResponseWriter, req *http.Request) {
		user := req.Header.Get("X-Etcd-UI-User")
		if !aclCanEdit(acl, user) {
			httpx.Err(w, 403, errSimple("undo requires admin on cluster __acl__"))
			return
		}
		var body struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.ID == "" {
			httpx.Err(w, 400, errSimple("missing id"))
			return
		}
		// Snapshot *before* image so undo is itself undo-able.
		_, _ = acl.SnapshotHistory(user, "before-undo", nil)
		path := filepath.Join(acl.HistoryDir(), filepath.Base(body.ID))
		if err := acl.Restore(path); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		log.Info("ACL undo applied", zap.String("by", user), zap.String("snapshot", body.ID))
		httpx.JSON(w, 200, map[string]any{"ok": true})
	})
	// Write-mode. Only admins (per the live ACL) may apply edits — so the
	// first set of rules must still come from the file/env (boot-strap).
	r.Put("/api/acl", func(w http.ResponseWriter, req *http.Request) {
		if acl.Path() == "" {
			httpx.Err(w, 400, errSimple("ACL not loaded from a writable file"))
			return
		}
		user := req.Header.Get("X-Etcd-UI-User")
		if user == "" {
			httpx.Err(w, 401, errSimple("unauthenticated"))
			return
		}
		// Editing ACL requires admin on the synthetic __acl__ cluster, or
		// wildcard-admin during bootstrap. See aclCanEdit().
		if !aclCanEdit(acl, user) {
			httpx.Err(w, 403, errSimple("editing ACL requires admin on cluster __acl__"))
			return
		}
		var rules []httpx.ACLRule
		if err := json.NewDecoder(req.Body).Decode(&rules); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		diff := aclDiff(acl.Rules(), rules)
		_, _ = acl.SnapshotHistory(user, "before", nil)
		if err := acl.Save(rules); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		afterID, _ := acl.SnapshotHistory(user, "after", rules)
		log.Info("ACL edited",
			zap.String("by", user),
			zap.Int("rules", len(rules)),
			zap.Int("added", diff.Added),
			zap.Int("changed", diff.Changed),
			zap.Int("removed", diff.Removed))
		// Audit trail.  The audit emitter middleware reads these headers and
		// turns them into a single audit event with a structured Note:
		//   prev=<base64 of diff JSON>   short summary, ≤ 4 KB
		//   link=acl-history:<filename>  pointer to the full snapshot on disk
		// The Audit UI parses the link and renders a "view snapshot" button
		// for action=="acl.edit" rows.
		w.Header().Set("X-Etcd-UI-Key", "__acl__")
		w.Header().Set("X-Etcd-UI-Prev-Value", base64.StdEncoding.EncodeToString(diff.JSON()))
		if afterID != "" {
			w.Header().Set("X-Etcd-UI-Note-Link", "acl-history:"+afterID)
		}
		httpx.JSON(w, 200, map[string]any{"ok": true, "rules": len(rules)})
	})
	r.Get("/api/auth/providers", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, 200, map[string]any{
			"basic": a.Enabled(),
			"oidc":  a.OIDCLoginConfigured(),
		})
	})

	r.Get("/api/version", func(w http.ResponseWriter, _ *http.Request) {
		build := Version
		if build == "" || build == "dev" {
			// Pre-baked at image build time. Falls back to the executable's
			// modtime if we couldn't inject anything via -ldflags (e.g. when
			// somebody runs `docker build` without --build-arg BUILD_SHA).
			if t := exeModTime(); !t.IsZero() {
				build = "img-" + t.UTC().Format("20060102-150405")
			}
		}
		httpx.JSON(w, 200, map[string]any{
			"app":     "etcd-ui",
			"version": "v0.1",
			"build":   build,
			"goEnv":   os.Getenv("ETCD_UI_BUILD"),
			"features": map[string]bool{
				// SPA reads this to surface upfront banners instead of
				// waiting for the user to hit a 403 (or worse, see a
				// silent fail).
				"etcdctl":       os.Getenv("ETCD_UI_ETCDCTL") == "on",
				"federationHub": os.Getenv("ETCD_UI_PEERS") != "",
				"snapshotS3":    os.Getenv("ETCD_UI_SNAPSHOT_S3_BUCKET") != "",
				"alertBrowser":  os.Getenv("ETCD_UI_ALERT_BROWSER") == "on",
			},
		})
	})

	// pprof, gated behind auth (a.Middleware already runs above).
	if os.Getenv("ETCD_UI_PPROF") == "on" {
		r.Get("/debug/pprof/", pprof.Index)
		r.Get("/debug/pprof/cmdline", pprof.Cmdline)
		r.Get("/debug/pprof/profile", pprof.Profile)
		r.Get("/debug/pprof/symbol", pprof.Symbol)
		r.Get("/debug/pprof/trace", pprof.Trace)
		r.Get("/debug/pprof/{name}", func(w http.ResponseWriter, req *http.Request) {
			pprof.Handler(chi.URLParam(req, "name")).ServeHTTP(w, req)
		})
		log.Info("pprof enabled at /debug/pprof")
	}

	// All /api/* requests are routed by a single handler so we can decide
	// based on the full original URL (chi.Mount strips prefixes).
	r.Handle("/api/*", apiRouter(clusterProxy, kvProxy, opsProxy, auditProxy))

	// Static SPA fallback.
	fileSrv := http.FileServer(http.Dir(cfg.WebRoot))
	r.Handle("/assets/*", fileSrv)
	r.Handle("/favicon.ico", fileSrv)
	r.Handle("/icon.svg", fileSrv)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api") {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(filepath.Join(cfg.WebRoot, "index.html"))
		if err != nil {
			http.Error(w, "frontend not built", http.StatusNotFound)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.Copy(w, f)
	})

	srv := &http.Server{
		Addr:              cfg.GatewayAddr,
		Handler:           tracing.WrapHandler("etcd-ui.gateway", r),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	tlsCert := os.Getenv("ETCD_UI_TLS_CERT")
	tlsKey := os.Getenv("ETCD_UI_TLS_KEY")
	go func() {
		log.Info("gateway listening", zap.String("addr", cfg.GatewayAddr), zap.String("version", Version))
		var err error
		if tlsCert != "" && tlsKey != "" {
			log.Info("TLS enabled", zap.String("cert", tlsCert))
			err = srv.ListenAndServeTLS(tlsCert, tlsKey)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatal("listen", zap.Error(err))
		}
	}()

	<-ctx.Done()
	sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(sh)
}

func newProxy(target *url.URL) *httputil.ReverseProxy {
	p := httputil.NewSingleHostReverseProxy(target)
	p.FlushInterval = 50 * time.Millisecond // important for SSE streams
	p.Transport = tracing.WrapTransport(http.DefaultTransport)
	d := p.Director
	p.Director = func(r *http.Request) {
		// Pluck the deferred cookie value off the request before forwarding —
		// the upstream service has no use for it and doesn't need to see our
		// auth bookkeeping.
		deferred := r.Header.Get(auth.WSDeferredCookieHeader)
		r.Header.Del(auth.WSDeferredCookieHeader)
		d(r)
		r.Host = target.Host
		if deferred != "" {
			// Stash for ModifyResponse on the same request lifecycle.
			r.Header.Set(auth.WSDeferredCookieHeader+"-Pending", deferred)
		}
	}
	p.ModifyResponse = func(resp *http.Response) error {
		// On a 101 Switching Protocols, re-emit the deferred Set-Cookie so
		// the browser stores a normal session cookie alongside the WS upgrade.
		if resp.StatusCode != http.StatusSwitchingProtocols {
			return nil
		}
		val := resp.Request.Header.Get(auth.WSDeferredCookieHeader + "-Pending")
		if val == "" {
			return nil
		}
		secure := "; Secure"
		if resp.Request.TLS == nil && resp.Request.Header.Get("X-Forwarded-Proto") != "https" {
			secure = "" // local dev / plain HTTP — don't fail the cookie
		}
		// Hard-coded cookie attributes mirror SessionCookie.Issue. Keeping
		// them inline avoids exposing a setter that callers might misuse.
		resp.Header.Add("Set-Cookie",
			"etcd-ui-session="+val+
				"; Path=/; HttpOnly; SameSite=Lax"+secure+
				"; Max-Age=3600")
		return nil
	}
	return p
}

// apiRouter dispatches /api/* requests by inspecting the URL path.
func apiRouter(clusterP, kvP, opsP, auditP *httputil.ReverseProxy) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// /api/audit/* -> audit
		if strings.HasPrefix(path, "/api/audit") {
			r.URL.Path = strings.TrimPrefix(path, "/api/audit")
			if r.URL.Path == "" {
				r.URL.Path = "/"
			}
			auditP.ServeHTTP(w, r)
			return
		}

		// /api/alerts/* -> cluster service
		if strings.HasPrefix(path, "/api/alerts") {
			r.URL.Path = strings.TrimPrefix(path, "/api")
			clusterP.ServeHTTP(w, r)
			return
		}

		// /api/clusters[/...] -> cluster/kv/ops
		if strings.HasPrefix(path, "/api/clusters") {
			// strip the /api prefix only; upstreams expect /clusters/{id}/...
			r.URL.Path = strings.TrimPrefix(path, "/api")
			segs := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			// segs: ["clusters"], ["clusters","{id}"], ["clusters","{id}","<verb>",...]
			if len(segs) < 3 {
				clusterP.ServeHTTP(w, r)
				return
			}
			switch segs[2] {
			case "range", "put", "put-cas", "put-k8s", "delete", "watch", "bulk", "txn", "export", "history", "diff":
				kvP.ServeHTTP(w, r)
			case "snapshot", "snapshots", "restore", "leases", "compact", "defrag", "alarms", "rbac", "metrics", "etcdctl", "etcdutl", "locks", "move-leader":
				opsP.ServeHTTP(w, r)
			default:
				clusterP.ServeHTTP(w, r)
			}
			return
		}

		http.NotFound(w, r)
	})
}

// ------ Audit emission middleware ------

// auditEmitter buffers events for up to 500ms (or 64 events) and POSTs them
// as a single batch to the audit service. This avoids 1-rpc-per-write at the
// hot path. Events are dropped on overflow rather than blocking handlers.
func auditEmitter(log *zap.Logger, auditURL string, m *httpx.Metrics) func(http.Handler) http.Handler {
	cli := &http.Client{
		Timeout:   3 * time.Second,
		Transport: tracing.WrapTransport(http.DefaultTransport),
	}
	ch := make(chan auditEvent, 1024)

	flush := func(buf []auditEvent) {
		if len(buf) == 0 {
			return
		}
		// Audit service currently accepts one event per POST; loop without
		// reopening the connection (Go's http client pools by default).
		for _, ev := range buf {
			body, _ := json.Marshal(ev)
			req, _ := http.NewRequest(http.MethodPost, auditURL+"/events", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := cli.Do(req)
			if err != nil {
				log.Debug("audit post failed", zap.Error(err))
				continue
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}

	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		buf := make([]auditEvent, 0, 64)
		for {
			select {
			case ev := <-ch:
				buf = append(buf, ev)
				if len(buf) >= 64 {
					flush(buf)
					buf = buf[:0]
				}
			case <-ticker.C:
				if len(buf) > 0 {
					flush(buf)
					buf = buf[:0]
				}
			}
		}
	}()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			if !isMutation(r) {
				return
			}
			ev := buildEvent(r, ww.Status())
			if k := ww.Header().Get("X-Etcd-UI-Key"); k != "" {
				ev.Key = k
			}
			if pv := ww.Header().Get("X-Etcd-UI-Prev-Value"); pv != "" {
				// Stored base64-encoded to survive header byte restrictions; the
				// audit UI decodes on display. Long values are pre-truncated to
				// 4KB at the source.
				ev.Note = "prev=" + pv
			}
			// Optional structured link to an off-event artifact (e.g. the
			// acl-history snapshot for action=acl.edit). Appended to Note as
			// `link=<scheme>:<id>` so the UI can render a "view" button.
			if link := ww.Header().Get("X-Etcd-UI-Note-Link"); link != "" {
				if ev.Note != "" {
					ev.Note += " "
				}
				ev.Note += "link=" + link
			}

			// Bulk-delete fan-out. kv emits an X-Etcd-UI-Bulk-Prev header that
			// base64-encodes a small JSON array of {k, v} entries. We expand
			// each into its own audit event so the Audit UI can offer Restore
			// per-key. Anything beyond the kv-side cap is already dropped.
			if bp := ww.Header().Get("X-Etcd-UI-Bulk-Prev"); bp != "" {
				if events := expandBulkPrev(ev, bp); len(events) > 0 {
					for _, be := range events {
						select {
						case ch <- be:
						default:
							m.AuditDropped(1)
						}
					}
					return
				}
			}

			select {
			case ch <- ev:
			default:
				m.AuditDropped(1)
				log.Warn("audit channel saturated, dropping event")
			}
		})
	}
}

// expandBulkPrev decodes the X-Etcd-UI-Bulk-Prev header into one auditEvent
// per captured key, copying the parent event's actor/path/status/etc. Returns
// nil if decoding fails (caller falls back to the single envelope event).
func expandBulkPrev(parent auditEvent, header string) []auditEvent {
	raw, err := base64Decode(header)
	if err != nil {
		return nil
	}
	var entries []struct {
		K string `json:"k"`
		V string `json:"v"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil
	}
	out := make([]auditEvent, 0, len(entries))
	for _, e := range entries {
		copy := parent
		copy.Key = e.K
		copy.Action = "kv.delete"
		copy.Note = "prev=" + e.V
		out = append(out, copy)
	}
	return out
}

func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// errSimple is a one-line error type for inline error literals.
type errSimple string

func (e errSimple) Error() string { return string(e) }

// aclChange is one row of an ACL audit diff. We don't try to be fancy
// (no field-level highlighting) — just "this rule was added / removed /
// changed", which is enough for forensics. JSON-encoded into the audit
// event's Note.
type aclChange struct {
	Op    string         `json:"op"` // "add" | "del" | "chg"
	Rule  httpx.ACLRule  `json:"rule,omitempty"`
	Prior *httpx.ACLRule `json:"prior,omitempty"` // only for chg
}

type aclDiffResult struct {
	Added   int         `json:"added"`
	Removed int         `json:"removed"`
	Changed int         `json:"changed"`
	Diff    []aclChange `json:"diff,omitempty"`
}

// JSON returns the diff body small enough to fit in an audit-event Note
// (4 KB hard cap). Over-budget diffs drop the per-row Diff array and keep
// only counters — the file itself is the source of truth anyway.
func (d aclDiffResult) JSON() []byte {
	b, _ := json.Marshal(d)
	if len(b) > 3500 {
		stripped := aclDiffResult{Added: d.Added, Removed: d.Removed, Changed: d.Changed}
		b, _ = json.Marshal(stripped)
	}
	return b
}

func aclRuleKey(r httpx.ACLRule) string {
	return r.User + "\x00" + r.Cluster + "\x00" + r.Prefix
}

func aclDiff(before, after []httpx.ACLRule) aclDiffResult {
	bMap := make(map[string]httpx.ACLRule, len(before))
	for _, r := range before {
		bMap[aclRuleKey(r)] = r
	}
	aMap := make(map[string]httpx.ACLRule, len(after))
	for _, r := range after {
		aMap[aclRuleKey(r)] = r
	}
	var out aclDiffResult
	for k, r := range aMap {
		prev, hadPrev := bMap[k]
		if !hadPrev {
			out.Added++
			out.Diff = append(out.Diff, aclChange{Op: "add", Rule: r})
		} else if prev.Access != r.Access {
			out.Changed++
			p := prev
			out.Diff = append(out.Diff, aclChange{Op: "chg", Rule: r, Prior: &p})
		}
	}
	for k, r := range bMap {
		if _, ok := aMap[k]; !ok {
			out.Removed++
			out.Diff = append(out.Diff, aclChange{Op: "del", Rule: r})
		}
	}
	return out
}

// aclCanEdit returns true if `user` is allowed to edit the ACL itself.
// Editing requires admin on the synthetic `__acl__` cluster — explicit
// opt-in via a dedicated rule, so "admin on prod" doesn't bleed into
// "can rewrite who has admin on prod". When no `__acl__` rule exists at
// all, we fall back to the bootstrap rule: any `cluster:"*"` admin rule
// also counts, so first-run setups (one wildcard admin) still work.
func aclCanEdit(a *httpx.ACL, user string) bool {
	hasExplicit := false
	hasWildcardAdmin := false
	for _, r := range a.Rules() {
		if r.Cluster == "__acl__" {
			hasExplicit = true
			if r.User == user && r.Access == httpx.AccessAdmin {
				return true
			}
		}
		if r.User == user && r.Cluster == "*" && r.Access == httpx.AccessAdmin {
			hasWildcardAdmin = true
		}
	}
	// Bootstrap path: no one has been granted __acl__ yet, so accept the
	// holder of the wildcard-admin rule. Once an __acl__ rule appears, this
	// implicit grant goes away.
	if !hasExplicit && hasWildcardAdmin {
		return true
	}
	return false
}

type auditEvent struct {
	Time      time.Time `json:"time"`
	Actor     string    `json:"actor"`
	Source    string    `json:"source"`
	Method    string    `json:"method"`
	Cluster   string    `json:"cluster,omitempty"`
	Path      string    `json:"path"`
	Action    string    `json:"action"`
	Key       string    `json:"key,omitempty"`
	Status    int       `json:"status"`
	IP        string    `json:"ip,omitempty"`
	UserAgent string    `json:"userAgent,omitempty"`
	Note      string    `json:"note,omitempty"`
}

func isMutation(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodOptions || r.Method == http.MethodHead {
		return false
	}
	if !strings.HasPrefix(r.URL.Path, "/api") {
		return false
	}
	// don't audit the audit service itself
	if strings.HasPrefix(r.URL.Path, "/api/audit") {
		return false
	}
	return true
}

func buildEvent(r *http.Request, status int) auditEvent {
	path := r.URL.Path
	cluster, action := classify(path, r.Method)
	actor := r.Header.Get("X-Etcd-UI-User")
	if actor == "" {
		actor = "anonymous"
	}
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.RemoteAddr
	}
	return auditEvent{
		Time:      time.Now().UTC(),
		Actor:     actor,
		Source:    "gateway",
		Method:    r.Method,
		Cluster:   cluster,
		Path:      path,
		Action:    action,
		Status:    status,
		IP:        ip,
		UserAgent: r.UserAgent(),
	}
}

func classify(path, method string) (cluster, action string) {
	// ACL edits get their own clean action name so the Audit UI can filter
	// and render the "view snapshot" affordance.
	if path == "/api/acl" {
		switch method {
		case http.MethodPut:
			return "", "acl.edit"
		case http.MethodPost:
			return "", "acl.restore"
		}
	}
	// matches /api/clusters/{id}/<verb...>
	if !strings.HasPrefix(path, "/api/clusters") {
		return "", method + " " + path
	}
	trimmed := strings.TrimPrefix(path, "/api/clusters")
	trimmed = strings.TrimPrefix(trimmed, "/")
	if trimmed == "" {
		return "", "cluster.create-or-list"
	}
	parts := strings.Split(trimmed, "/")
	cluster = parts[0]
	if len(parts) == 1 {
		switch method {
		case http.MethodDelete:
			return cluster, "cluster.remove"
		}
		return cluster, "cluster.update"
	}
	verb := parts[1]
	switch verb {
	case "put":
		return cluster, "kv.put"
	case "delete":
		return cluster, "kv.delete"
	case "bulk":
		if len(parts) > 2 {
			return cluster, "kv.bulk." + parts[2]
		}
		return cluster, "kv.bulk"
	case "txn":
		return cluster, "kv.txn"
	case "snapshot":
		return cluster, "ops.snapshot"
	case "restore":
		return cluster, "ops.restore"
	case "compact":
		return cluster, "ops.compact"
	case "defrag":
		return cluster, "ops.defrag"
	case "alarms":
		return cluster, "ops.alarms.disarm"
	case "leases":
		return cluster, "ops.leases.revoke"
	case "rbac":
		if len(parts) > 2 {
			return cluster, "rbac." + parts[2]
		}
		return cluster, "rbac"
	case "history":
		return cluster, "kv.history"
	case "diff":
		return cluster, "kv.diff"
	case "metrics":
		return cluster, "ops.metrics"
	case "etcdctl":
		return cluster, "ops.etcdctl"
	}
	return cluster, verb
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			return n
		}
	}
	return def
}
