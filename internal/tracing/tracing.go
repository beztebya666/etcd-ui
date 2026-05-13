// Package tracing wires OpenTelemetry across all five services.
//
// Behaviour:
//
//   - If OTEL_EXPORTER_OTLP_ENDPOINT is set, traces are exported over HTTP
//     (OTLP/HTTP) to that collector. Otherwise the SDK is installed with a
//     noop exporter — context still propagates between services so logs stay
//     correlated, but nothing is shipped externally.
//   - W3C TraceContext + Baggage propagation, so incoming `traceparent`
//     headers continue spans and outgoing HTTP clients re-emit them.
//   - Resource attributes pin service.name and version so the collector can
//     route by service.
//
// Usage:
//
//	shutdown, err := tracing.Init(ctx, "gateway", Version)
//	defer shutdown(context.Background())
package tracing

import (
	"context"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Init installs a global TracerProvider and Propagator for the given service.
// Returns a shutdown function that flushes any in-flight spans.
func Init(ctx context.Context, service, version string) (func(context.Context) error, error) {
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

	var sp sdktrace.SpanProcessor
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(stripScheme(endpoint))}
		if isInsecure(endpoint) {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		exp, err := otlptrace.New(ctx, otlptracehttp.NewClient(opts...))
		if err != nil {
			return noop, err
		}
		sp = sdktrace.NewBatchSpanProcessor(exp,
			sdktrace.WithMaxExportBatchSize(256),
			sdktrace.WithBatchTimeout(2*time.Second),
		)
	}

	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(samplingRatio()))),
	}
	if sp != nil {
		tpOpts = append(tpOpts, sdktrace.WithSpanProcessor(sp))
	}
	tp := sdktrace.NewTracerProvider(tpOpts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return tp.Shutdown(ctx)
	}, nil
}

// WrapHandler instruments an http.Handler with HTTP server spans.
func WrapHandler(name string, h http.Handler) http.Handler {
	return otelhttp.NewHandler(h, name)
}

// WrapTransport wraps an http.RoundTripper so outgoing requests carry trace
// context and emit client spans.
func WrapTransport(rt http.RoundTripper) http.RoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}
	return otelhttp.NewTransport(rt)
}

// EtcdSpan opens a child span for an etcd client call. Names are namespaced
// (`etcd.range`, `etcd.put`, etc.) so the trace tree clearly distinguishes
// HTTP-handler boundaries from the actual etcd round trip. Attributes carry
// the cluster id and (when known) the touched key — those become first-class
// filters in Jaeger/Tempo.
//
// Usage:
//
//	ctx, end := tracing.EtcdSpan(r.Context(), "range", id, attribute.String("etcd.key", req.Prefix))
//	defer end()
//	resp, err := cli.Get(ctx, key, opts...)
//	end(err) // optional: end() also accepts an error for status
func EtcdSpan(ctx context.Context, op, clusterID string, attrs ...attribute.KeyValue) (context.Context, func(...error)) {
	tr := otel.Tracer("etcd-ui/etcd")
	all := append([]attribute.KeyValue{
		attribute.String("etcd.op", op),
		attribute.String("etcd.cluster", clusterID),
	}, attrs...)
	ctx, span := tr.Start(ctx, "etcd."+op, trace.WithAttributes(all...))
	return ctx, func(errs ...error) {
		for _, err := range errs {
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				break
			}
		}
		span.End()
	}
}

func noop(_ context.Context) error { return nil }

func samplingRatio() float64 {
	v := os.Getenv("OTEL_TRACES_SAMPLER_ARG")
	if v == "" {
		return 1.0
	}
	// rudimentary; full sampler config is out of scope.
	switch v {
	case "0", "0.0":
		return 0
	case "0.1":
		return 0.1
	case "0.5":
		return 0.5
	}
	return 1.0
}

func stripScheme(s string) string {
	for _, p := range []string{"http://", "https://"} {
		if len(s) > len(p) && s[:len(p)] == p {
			return s[len(p):]
		}
	}
	return s
}

func isInsecure(s string) bool {
	return len(s) >= 7 && s[:7] == "http://"
}
