package httpx

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// ReadinessProbe pings a set of upstream URLs and reports overall readiness.
// `Ready()` is cheap (atomic read); a background goroutine refreshes the flag.
type ReadinessProbe struct {
	upstreams []string
	ready     atomic.Bool
	client    *http.Client
	period    time.Duration
}

func NewReadinessProbe(upstreams []string) *ReadinessProbe {
	return &ReadinessProbe{
		upstreams: upstreams,
		client:    &http.Client{Timeout: 2 * time.Second},
		period:    3 * time.Second,
	}
}

func (rp *ReadinessProbe) Start(ctx context.Context) {
	check := func() {
		for _, u := range rp.upstreams {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u+"/healthz", nil)
			resp, err := rp.client.Do(req)
			if err != nil || resp.StatusCode != 200 {
				rp.ready.Store(false)
				if resp != nil {
					resp.Body.Close()
				}
				return
			}
			resp.Body.Close()
		}
		rp.ready.Store(true)
	}
	check()
	go func() {
		t := time.NewTicker(rp.period)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				check()
			}
		}
	}()
}

func (rp *ReadinessProbe) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if rp.ready.Load() {
			w.WriteHeader(200)
			_, _ = w.Write([]byte("ready"))
			return
		}
		w.WriteHeader(503)
		_, _ = w.Write([]byte("not ready"))
	}
}
