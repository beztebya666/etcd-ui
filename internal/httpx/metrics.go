package httpx

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

func otelMeter(name string) metric.Meter { return otel.Meter(name) }

func otelMethodStatus(method, status string) attribute.Set {
	return attribute.NewSet(
		attribute.String("method", method),
		attribute.String("status", status),
	)
}

// Metrics is a tiny zero-dep Prometheus exposition aggregator scoped to the
// gateway itself. Reports request totals, status code histogram and a sum/
// count latency for naive averaging. For real production scrape add the
// prometheus client_golang lib — but this covers the basic SRE need.
type Metrics struct {
	mu               sync.Mutex
	total            map[string]uint64 // method+statusBucket -> count
	durSumMicros     atomic.Uint64
	durCount         atomic.Uint64
	inflight         atomic.Int64
	auditDropped     atomic.Uint64
}

func NewMetrics() *Metrics {
	return &Metrics{total: map[string]uint64{}}
}

func (m *Metrics) AuditDropped(n uint64) { m.auditDropped.Add(n) }

func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" || r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		m.inflight.Add(1)
		defer m.inflight.Add(-1)
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		dur := time.Since(start)

		bucket := fmt.Sprintf("%dxx", ww.Status()/100)
		key := r.Method + "_" + bucket
		m.mu.Lock()
		m.total[key]++
		m.mu.Unlock()

		m.durSumMicros.Add(uint64(dur.Microseconds()))
		m.durCount.Add(1)
	})
}

// RegisterOTel publishes the same counters/gauges as the Prometheus /metrics
// endpoint, but via the global OTel MeterProvider. Safe no-op if InitMetrics
// was never called (the global provider falls back to a noop meter). Returns
// the registration error so callers can log it; instruments stay registered
// for the process lifetime.
func (m *Metrics) RegisterOTel(meterName string) error {
	meter := otelMeter(meterName)

	reqTotal, err := meter.Int64ObservableCounter(
		"etcd_ui_requests_total",
		metric.WithDescription("HTTP requests handled by the gateway."),
	)
	if err != nil {
		return err
	}
	durSum, err := meter.Int64ObservableCounter(
		"etcd_ui_request_duration_microseconds_sum",
		metric.WithUnit("us"),
	)
	if err != nil {
		return err
	}
	durCount, err := meter.Int64ObservableCounter("etcd_ui_request_duration_microseconds_count")
	if err != nil {
		return err
	}
	inflight, err := meter.Int64ObservableGauge("etcd_ui_requests_inflight")
	if err != nil {
		return err
	}
	dropped, err := meter.Int64ObservableCounter("etcd_ui_audit_dropped_total")
	if err != nil {
		return err
	}

	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		m.mu.Lock()
		for k, v := range m.total {
			parts := strings.SplitN(k, "_", 2)
			method, status := parts[0], ""
			if len(parts) == 2 {
				status = parts[1]
			}
			o.ObserveInt64(reqTotal, int64(v),
				metric.WithAttributeSet(otelMethodStatus(method, status)),
			)
		}
		m.mu.Unlock()
		o.ObserveInt64(durSum, int64(m.durSumMicros.Load()))
		o.ObserveInt64(durCount, int64(m.durCount.Load()))
		o.ObserveInt64(inflight, m.inflight.Load())
		o.ObserveInt64(dropped, int64(m.auditDropped.Load()))
		return nil
	}, reqTotal, durSum, durCount, inflight, dropped)
	return err
}

func (m *Metrics) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		m.mu.Lock()
		defer m.mu.Unlock()
		var b strings.Builder
		b.WriteString("# HELP etcd_ui_requests_total HTTP requests handled by the gateway.\n")
		b.WriteString("# TYPE etcd_ui_requests_total counter\n")
		for k, v := range m.total {
			parts := strings.SplitN(k, "_", 2)
			fmt.Fprintf(&b, `etcd_ui_requests_total{method="%s",status="%s"} %d`+"\n", parts[0], parts[1], v)
		}
		fmt.Fprintf(&b, "# HELP etcd_ui_request_duration_microseconds_sum Sum of all request durations.\n# TYPE etcd_ui_request_duration_microseconds_sum counter\n")
		fmt.Fprintf(&b, "etcd_ui_request_duration_microseconds_sum %d\n", m.durSumMicros.Load())
		fmt.Fprintf(&b, "# HELP etcd_ui_request_duration_microseconds_count Number of requests timed.\n# TYPE etcd_ui_request_duration_microseconds_count counter\n")
		fmt.Fprintf(&b, "etcd_ui_request_duration_microseconds_count %d\n", m.durCount.Load())
		fmt.Fprintf(&b, "# HELP etcd_ui_requests_inflight Currently processing requests.\n# TYPE etcd_ui_requests_inflight gauge\netcd_ui_requests_inflight %d\n", m.inflight.Load())
		fmt.Fprintf(&b, "# HELP etcd_ui_audit_dropped_total Audit events dropped due to channel saturation.\n# TYPE etcd_ui_audit_dropped_total counter\netcd_ui_audit_dropped_total %d\n", m.auditDropped.Load())
		_, _ = w.Write([]byte(b.String()))
	}
}
