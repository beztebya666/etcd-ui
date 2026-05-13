// audit — fifth microservice. Stores audit events (writes + admin ops)
// performed via the UI. Backed by an on-disk JSONL file + in-memory ring.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/yourorg/etcd-ui/internal/audit"
	"github.com/yourorg/etcd-ui/internal/config"
	"github.com/yourorg/etcd-ui/internal/httpx"
	"github.com/yourorg/etcd-ui/internal/logger"
	"github.com/yourorg/etcd-ui/internal/tracing"

	"go.uber.org/zap"
)

// Version is set at build time via -ldflags.
var Version = "dev"

func main() {
	log := logger.New("audit")
	cfg := config.Load()

	dataDir := cfg.DataDir
	store, err := audit.New(dataDir, 5000)
	if err != nil {
		// Falling back to a tempdir keeps the service operational on dev hosts
		// where /app/data isn't writable; warn loudly.
		tmp, terr := os.MkdirTemp("", "etcd-ui-audit-")
		if terr != nil {
			log.Fatal("audit store + tempdir", zap.Error(err))
		}
		log.Warn("audit data dir not writable, using tempdir",
			zap.String("requested", dataDir),
			zap.String("fallback", tmp),
			zap.Error(err))
		store, err = audit.New(tmp, 5000)
		if err != nil {
			log.Fatal("audit store fallback", zap.Error(err))
		}
	}
	defer store.Close()

	// Retention overrides — env-tunable. Defaults: 256MiB / 30d.
	if v := os.Getenv("ETCD_UI_AUDIT_MAX_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			store.SetRetention(n, 0)
		}
	}
	if v := os.Getenv("ETCD_UI_AUDIT_MAX_AGE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			store.SetRetention(0, d)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Compaction runs every hour. First run is delayed 30s so we don't compete
	// with startup work; subsequent runs are silent unless something changes.
	go func() {
		t := time.NewTimer(30 * time.Second)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				before, after, dropped, err := store.Compact()
				if err != nil {
					log.Warn("audit compact failed", zap.Error(err))
				} else if before != after || dropped > 0 {
					log.Info("audit compacted",
						zap.Int64("bytesBefore", before),
						zap.Int64("bytesAfter", after),
						zap.Int("dropped", dropped))
				}
				t.Reset(time.Hour)
			}
		}
	}()

	traceShutdown, terr := tracing.Init(ctx, "audit", Version)
	if terr != nil {
		log.Warn("tracing init failed", zap.Error(terr))
	}
	defer traceShutdown(context.Background())

	// fanout: any goroutine doing /events/stream gets new events pushed.
	hub := newHub()

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer, middleware.StripSlashes, httpx.Logging(log))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })

	r.Post("/events", func(w http.ResponseWriter, r *http.Request) {
		var e audit.Event
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		ev := store.Append(e)
		hub.broadcast(ev)
		httpx.JSON(w, 201, ev)
	})

	// Stream the full JSONL log as an attachment. Useful for offline grep /
	// compliance archival. Skips when persistence is disabled (returns 204).
	r.Get("/events/download", func(w http.ResponseWriter, r *http.Request) {
		path := store.LogPath()
		if path == "" {
			w.WriteHeader(204)
			return
		}
		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				w.WriteHeader(204)
				return
			}
			httpx.Err(w, 500, err)
			return
		}
		defer f.Close()
		name := "audit-" + time.Now().UTC().Format("20060102T150405Z") + ".jsonl"
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		if st, err := f.Stat(); err == nil {
			w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
		}
		_, _ = io.Copy(w, f)
	})

	// Manual compaction trigger — exposed mostly for ops UX; the goroutine
	// already runs hourly.
	r.Post("/compact", func(w http.ResponseWriter, _ *http.Request) {
		before, after, dropped, err := store.Compact()
		if err != nil {
			httpx.Err(w, 500, err)
			return
		}
		httpx.JSON(w, 200, map[string]any{
			"bytesBefore": before,
			"bytesAfter":  after,
			"dropped":     dropped,
		})
	})

	r.Get("/events", func(w http.ResponseWriter, r *http.Request) {
		limit := 200
		if v := r.URL.Query().Get("limit"); v != "" {
			n, _ := strconv.Atoi(v)
			if n > 0 {
				limit = n
			}
		}
		if since := r.URL.Query().Get("since"); since != "" {
			id, _ := strconv.ParseInt(since, 10, 64)
			httpx.JSON(w, 200, store.Since(id, limit))
			return
		}
		httpx.JSON(w, 200, store.Tail(limit))
	})

	// Server-Sent Events live tail.
	// Dual-transport stream: WebSocket-first, SSE fallback. Same shape as
	// the alerts stream — the SPA's lib/stream wrapper drives both.
	r.Get("/events/stream", func(w http.ResponseWriter, r *http.Request) {
		ch := hub.subscribe()
		defer hub.unsubscribe(ch)
		prime := reverse(store.Tail(50))

		if ws, err := httpx.Upgrade(w, r); err == nil {
			defer ws.Close()
			for _, ev := range prime {
				b, _ := json.Marshal(ev)
				if err := ws.SendText(b); err != nil {
					return
				}
			}
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
		for _, ev := range prime {
			b, _ := json.Marshal(ev)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		}
		flusher.Flush()

		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
				_, _ = fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			case ev := <-ch:
				b, _ := json.Marshal(ev)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
				flusher.Flush()
			}
		}
	})

	srv := &http.Server{Addr: cfg.AuditAddr, Handler: tracing.WrapHandler("etcd-ui.audit", r)}
	go func() {
		log.Info("audit service listening", zap.String("addr", cfg.AuditAddr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("listen", zap.Error(err))
		}
	}()
	<-ctx.Done()
	sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(sh)
}

func reverse(in []audit.Event) []audit.Event {
	out := make([]audit.Event, len(in))
	for i, e := range in {
		out[len(in)-1-i] = e
	}
	return out
}

// --- pub/sub hub for SSE streams ---

type hub struct {
	mu   sync.RWMutex
	subs map[chan audit.Event]struct{}
}

func newHub() *hub { return &hub{subs: map[chan audit.Event]struct{}{}} }

func (h *hub) subscribe() chan audit.Event {
	ch := make(chan audit.Event, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(ch chan audit.Event) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
	close(ch)
}

func (h *hub) broadcast(e audit.Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
			// drop on slow consumer; UI re-syncs on reconnect
		}
	}
}
