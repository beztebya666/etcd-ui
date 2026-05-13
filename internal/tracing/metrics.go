// OTel metrics bridge. Standalone from tracing.go so traces and metrics
// pipelines stay independent — you can ship one without the other depending
// on collector capabilities.
//
// Toggle via env:
//
//	OTEL_EXPORTER_OTLP_METRICS_ENDPOINT — full URL of an OTLP/HTTP metrics
//	collector. If unset, a noop meter is installed and Observe() is free.
//	OTEL_METRICS_INTERVAL — periodic export cadence (default 30s).

package tracing

import (
	"context"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// InitMetrics installs a global MeterProvider for the service. Returns a
// shutdown function that flushes the periodic exporter.
func InitMetrics(ctx context.Context, service, version string) (func(context.Context) error, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(service),
			semconv.ServiceVersion(version),
			semconv.ServiceNamespace("etcd-ui"),
		),
	)
	if err != nil {
		return noop, err
	}

	opts := []sdkmetric.Option{sdkmetric.WithResource(res)}

	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"); endpoint != "" {
		expOpts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpoint(stripScheme(endpoint))}
		if isInsecure(endpoint) {
			expOpts = append(expOpts, otlpmetrichttp.WithInsecure())
		}
		exp, err := otlpmetrichttp.New(ctx, expOpts...)
		if err != nil {
			return noop, err
		}
		interval := 30 * time.Second
		if v := os.Getenv("OTEL_METRICS_INTERVAL"); v != "" {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				interval = d
			}
		}
		opts = append(opts,
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(interval))),
		)
	}

	mp := sdkmetric.NewMeterProvider(opts...)
	otel.SetMeterProvider(mp)
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return mp.Shutdown(ctx)
	}, nil
}

// Meter returns the named meter from the global provider. Safe to call from
// any service — if InitMetrics was never invoked, you get a noop meter.
func Meter(name string) metric.Meter {
	return otel.Meter(name)
}
