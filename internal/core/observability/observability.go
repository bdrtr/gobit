// Package observability sets up the OpenTelemetry trace and metric
// infrastructure.
//
// # When it is off, it is really off
//
// With no collector address given, no outbound connection is attempted and
// every telemetry call falls through to OTel's own no-op implementations. That
// is a deliberate choice so a development environment does not produce a
// constant "connection refused" noise; giving a collector address is an
// EXPLICIT decision.
//
// # A setup failure does not bring the application down
//
// When [Setup] cannot reach the collector the application opens anyway and
// telemetry stays off. The rationale is ADR 0007's: telemetry exists for the
// product's visibility, not its CORRECTNESS. An outage at the collector must
// not close the store. The gRPC exporter connects lazily anyway, so the real
// failure mode is at runtime rather than at startup, and there it retries
// silently.
//
// # The sampling decision is NOT left to the client
//
// The incoming traceparent header is read, but its "sampled" flag cannot
// override the sampling ratio; for the detail and the rationale see
// [sampler].
package observability

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// shutdownTimeout is the time allowed to EACH exporter SEPARATELY at shutdown.
//
// Sending the spans pending at shutdown is valuable, but waiting indefinitely
// means hanging SIGTERM while the collector is unreachable.
//
// That the budget is PER PROVIDER is deliberate; the rationale is in
// [runShutdown].
const shutdownTimeout = 5 * time.Second

// Options are the inputs of the telemetry setup.
type Options struct {
	// Endpoint is the OTLP collector's gRPC address. When empty, telemetry is
	// OFF.
	Endpoint string
	// Insecure says the connection is made without TLS.
	Insecure bool
	// ServiceName is the service name reported in traces and metrics.
	ServiceName string
	// ServiceVersion is the build version.
	ServiceVersion string
	// Environment is the runtime environment (development | staging |
	// production).
	Environment string
	// SampleRatio is the ratio of traces to sample (0.0 - 1.0).
	SampleRatio float64
	// MetricInterval is how often metrics are sent; zero means 60s.
	MetricInterval time.Duration
	// Logger writes the setup events; nil means slog.Default.
	Logger *slog.Logger
}

// ShutdownFunc closes the telemetry infrastructure.
type ShutdownFunc func(ctx context.Context) error

// Setup builds the global tracer and meter providers.
//
// The ShutdownFunc returned is always callable (it is NEVER nil), so the caller
// never has to write a conditional shutdown path even while telemetry is off. A
// conditional shutdown would turn into a nil pointer panic in a caller who
// forgot the "it returns nil when off" detail.
//
// An error is returned ONLY for a broken configuration; network
// unreachability is not an error, because the gRPC exporter connects lazily.
func Setup(ctx context.Context, opts Options) (ShutdownFunc, error) {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}

	if opts.Endpoint == "" {
		log.InfoContext(ctx, "no OTLP address was given, telemetry is off")

		return noopShutdown, nil
	}

	res, err := newResource(ctx, opts)
	if err != nil {
		return noopShutdown, err
	}

	tracerProvider, err := newTracerProvider(ctx, opts, res)
	if err != nil {
		return noopShutdown, err
	}

	meterProvider, err := newMeterProvider(ctx, opts, res)
	if err != nil {
		// The trace provider was built but the metric one could not be:
		// leaving a half setup behind means an orphaned goroutine still trying
		// to send at shutdown.
		_ = tracerProvider.Shutdown(ctx)

		return noopShutdown, err
	}

	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)

	// W3C TraceContext + Baggage: needed so the trace header coming from the
	// client can be continued. Without it every service produces its own
	// disconnected trace and distributed tracing joins nothing.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	log.InfoContext(ctx, "telemetry is set up",
		"endpoint", opts.Endpoint,
		"insecure", opts.Insecure,
		"sample_ratio", opts.SampleRatio)

	return func(ctx context.Context) error {
		return runShutdown(ctx, shutdownTimeout, tracerProvider, meterProvider)
	}, nil
}

// closable is the only behavior expected from an exporter at shutdown.
//
// Working through this interface instead of the concrete SDK types is what
// makes the budget sharing testable without bringing a real collector up.
type closable interface {
	Shutdown(ctx context.Context) error
}

// runShutdown closes the providers IN PARALLEL and gives each its OWN budget.
//
// Previously a single context was shared between the tracer and the meter:
// while the collector was unreachable the tracer ate the whole budget and the
// meter was called with a context that had already expired. The result was that
// the metrics pending at shutdown were dropped SILENTLY — and those were the
// most needed ones, belonging to the process's final moments.
//
// Splitting the budget in two (half each) would also solve the starvation, but
// in the common case where only ONE provider is slow it would condemn that one
// to half a budget for nothing. Closing them in sequence with a full budget
// each doubles the shutdown in the worst case; because the two providers use
// separate gRPC connections there is nothing to gain from serializing the wait.
// Hence parallel plus a full budget per provider: the total wait still stays
// bounded by a single budget.
func runShutdown(ctx context.Context, budget time.Duration, providers ...closable) error {
	failures := make([]error, len(providers))

	var wg sync.WaitGroup
	wg.Add(len(providers))

	for i, p := range providers {
		go func() {
			defer wg.Done()

			shutdownCtx, cancel := context.WithTimeout(ctx, budget)
			defer cancel()

			failures[i] = p.Shutdown(shutdownCtx)
		}()
	}
	wg.Wait()

	return errors.Join(failures...)
}

// endpointHasScheme reports whether the address carries a URL scheme.
//
// In the OpenTelemetry specification OTEL_EXPORTER_OTLP_ENDPOINT is a URL
// ("http://collector:4317"), while the Go SDK's WithEndpoint option expects a
// SCHEMELESS "host:port". Mixing the two produces NO error: gRPC connects
// lazily, the setup is logged as "successful" and the spans SILENTLY go
// nowhere.
//
// A silent loss is far more expensive than a noisy error: telemetry is believed
// to be on while it is off, and that is only noticed while a failure is being
// investigated — the worst possible moment. A variable that borrows the
// specification's name must accept the specification's value too; both forms
// are therefore supported.
func endpointHasScheme(endpoint string) bool {
	return strings.Contains(endpoint, "://")
}

// traceEndpoint hands the address to the trace exporter through the right
// option.
func traceEndpoint(endpoint string) otlptracegrpc.Option {
	if endpointHasScheme(endpoint) {
		return otlptracegrpc.WithEndpointURL(endpoint)
	}

	return otlptracegrpc.WithEndpoint(endpoint)
}

// metricEndpoint hands the address to the metric exporter through the right
// option.
func metricEndpoint(endpoint string) otlpmetricgrpc.Option {
	if endpointHasScheme(endpoint) {
		return otlpmetricgrpc.WithEndpointURL(endpoint)
	}

	return otlpmetricgrpc.WithEndpoint(endpoint)
}

// noopShutdown is the shutdown function used while telemetry is off.
func noopShutdown(context.Context) error { return nil }

// newResource builds the service identity attached to traces and metrics.
func newResource(ctx context.Context, opts Options) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(opts.ServiceName),
			semconv.ServiceVersion(opts.ServiceVersion),
			semconv.DeploymentEnvironmentNameKey.String(opts.Environment),
		),
	)
}

// newTracerProvider builds the tracer provider with an OTLP exporter.
func newTracerProvider(
	ctx context.Context, opts Options, res *resource.Resource,
) (*sdktrace.TracerProvider, error) {
	exporter := []otlptracegrpc.Option{traceEndpoint(opts.Endpoint)}
	if opts.Insecure {
		exporter = append(exporter, otlptracegrpc.WithInsecure())
	}

	exp, err := otlptracegrpc.New(ctx, exporter...)
	if err != nil {
		return nil, err
	}

	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler(opts.SampleRatio)),
	), nil
}

// sampler builds the sampler that makes the sampling decision.
//
// A REMOTE parent's "sampled" flag CANNOT override the ratio. On a public
// endpoint the traceparent header is entirely under the client's control; with
// ParentBased's default of [sdktrace.AlwaysSample], an attacker marking every
// request "sampled" makes the OTEL_TRACES_SAMPLER_ARG=0.01 setting meaningless
// and inflates the telemetry cost at will. When a remote parent arrives the
// decision is therefore recomputed with the SAME ratio.
//
// This does NOT riddle the distributed trace with holes:
// [sdktrace.TraceIDRatioBased] derives the decision from the trace ID itself,
// so every service using the same ratio reaches the same answer for the same
// trace. In an installation where the ratios differ between services the
// consistency is lost anyway; the right fix there is to align the ratios, not
// to trust the client.
//
// When the remote parent was not sampled we do not sample at all: with the
// upstream service's answer already "no", saying "yes" would produce parentless
// spans that are never completed.
//
// For a LOCAL parent, ParentBased's default is kept (we follow the parent
// span): letting a child span in the same process decide independently would
// fragment a single request's span tree within itself.
func sampler(ratio float64) sdktrace.Sampler {
	ratioBased := sdktrace.TraceIDRatioBased(ratio)

	return sdktrace.ParentBased(
		ratioBased,
		sdktrace.WithRemoteParentSampled(ratioBased),
		sdktrace.WithRemoteParentNotSampled(sdktrace.NeverSample()),
	)
}

// newMeterProvider builds the meter provider with an OTLP exporter.
func newMeterProvider(
	ctx context.Context, opts Options, res *resource.Resource,
) (*sdkmetric.MeterProvider, error) {
	exporter := []otlpmetricgrpc.Option{metricEndpoint(opts.Endpoint)}
	if opts.Insecure {
		exporter = append(exporter, otlpmetricgrpc.WithInsecure())
	}

	exp, err := otlpmetricgrpc.New(ctx, exporter...)
	if err != nil {
		return nil, err
	}

	interval := opts.MetricInterval
	if interval <= 0 {
		interval = time.Minute
	}

	return sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp,
			sdkmetric.WithInterval(interval))),
	), nil
}

// Attrs returns the common attributes to be added to slog records.
func Attrs(opts Options) []attribute.KeyValue {
	return []attribute.KeyValue{
		semconv.ServiceName(opts.ServiceName),
		semconv.ServiceVersion(opts.ServiceVersion),
	}
}
