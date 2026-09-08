// Package observability sets up the OpenTelemetry trace and metric
// infrastructure.
//
// # The two signals leave by two different doors
//
// Traces are PUSHED to an OTLP collector; metrics are PULLED from a scrape
// endpoint this package builds and internal/app serves (ADR 0046). Each door
// has its own switch — [Options.Endpoint] for the traces, [Options.Metrics] for
// the metrics — and neither switch touches the other signal.
//
// One address used to decide both, and that is how the metric half went
// missing quietly enough for two documents to describe it wrongly: an empty
// OTEL_EXPORTER_OTLP_ENDPOINT returned from [Setup] before EITHER provider was
// built, so the HTTP instruments in core/http recorded into OTel's no-ops in
// every stock installation while the configuration comment spoke only of
// tracing. Two switches are what keep that from being sayable again.
//
// # When it is off, it is really off
//
// With NEITHER an OTLP address nor a metrics endpoint asked for, nothing is
// built and every telemetry call falls through to OTel's own no-op
// implementations. That is a deliberate choice so a development environment
// does not produce a constant "connection refused" noise; asking for either
// signal is an EXPLICIT decision.
//
// The metric half keeps that promise for a reason worth stating: a PULL reader
// opens no outbound connection at all. Turning metrics on in a development
// environment therefore costs a listener and an aggregation store, and not one
// failed export per interval.
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
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/prometheus"
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

// MetricsPath is the only path [Telemetry.MetricsHandler] answers.
//
// It is a constant rather than a setting because a scrape configuration that
// has to be told where to look is a second thing to keep in agreement with the
// application, and the convention every scraper already assumes is this one.
const MetricsPath = "/metrics"

// Options are the inputs of the telemetry setup.
type Options struct {
	// Endpoint is the OTLP collector's gRPC address. When empty, TRACES are
	// OFF.
	//
	// It decides the TRACE signal ALONE (ADR 0046). Metrics used to ride on
	// this same address and no longer do; an installation that wants metrics
	// asks for them with [Options.Metrics].
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
	// Metrics says whether a meter provider is built and a scrape handler
	// handed back.
	//
	// It is a BOOLEAN and not the listener's address ON PURPOSE. This package
	// builds a handler and never binds a port, and a field holding an address
	// it does not listen on invites exactly the belief that it does. The rule
	// "no address, no metrics" therefore stays written in ONE place —
	// internal/app, beside the listener the address belongs to — and arrives
	// here already decided.
	Metrics bool
	// Logger writes the setup events; nil means slog.Default.
	Logger *slog.Logger
}

// ShutdownFunc closes the telemetry infrastructure.
type ShutdownFunc func(ctx context.Context) error

// Telemetry is what [Setup] leaves behind for the caller to hold.
//
// It is a struct rather than a second return value because the two things in
// it are answered by the same call and read at opposite ends of the process's
// life: the handler goes to a listener during boot, the shutdown runs at
// SIGTERM. A pair of bare returns would let a caller keep one and drop the
// other without the compiler noticing which.
type Telemetry struct {
	// Shutdown closes whichever providers were built.
	//
	// It is always callable (it is NEVER nil), so the caller never has to write
	// a conditional shutdown path even while telemetry is off. A conditional
	// shutdown would turn into a nil pointer panic in a caller who forgot the
	// "it returns nil when off" detail.
	Shutdown ShutdownFunc
	// MetricsHandler serves the scrape output at [MetricsPath], and NIL means
	// no metrics were asked for.
	//
	// The caller must treat nil as "open no listener". Serving a nil handler
	// would publish a port that answers 404 to every scrape, which reads to an
	// operator like a broken deployment rather than like a switch left off.
	MetricsHandler http.Handler
}

// Setup builds the global tracer and meter providers.
//
// Each provider is built only for the signal that asked for it: an OTLP
// endpoint builds the tracer, [Options.Metrics] builds the meter. With neither
// asked for, nothing is built and nothing global is touched.
//
// An error is returned ONLY for a broken configuration; network
// unreachability is not an error, because the gRPC exporter connects lazily.
func Setup(ctx context.Context, opts Options) (Telemetry, error) {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}

	// The early return's condition covers BOTH signals. When it covered only
	// the OTLP address it silenced metrics as a side effect of a decision about
	// traces, which is the defect ADR 0046 was written on top of.
	if opts.Endpoint == "" && !opts.Metrics {
		log.InfoContext(ctx, "no OTLP address and no metrics endpoint were asked for, telemetry is off")

		return Telemetry{Shutdown: noopShutdown}, nil
	}

	res, err := newResource(ctx, opts)
	if err != nil {
		return Telemetry{Shutdown: noopShutdown}, err
	}

	// The providers are collected as they are built so the shutdown closes
	// exactly what exists. Handing runShutdown a nil provider for the signal
	// that was never turned on would panic at SIGTERM — the one moment nobody
	// is watching the logs.
	var providers []closable

	var tracerProvider *sdktrace.TracerProvider

	if opts.Endpoint != "" {
		tracerProvider, err = newTracerProvider(ctx, opts, res)
		if err != nil {
			return Telemetry{Shutdown: noopShutdown}, err
		}

		providers = append(providers, tracerProvider)
	}

	var metricsHandler http.Handler

	if opts.Metrics {
		meterProvider, handler, err := newMeterProvider(res, log)
		if err != nil {
			// The trace provider may already have been built: leaving a half
			// setup behind means an orphaned goroutine still trying to send at
			// shutdown.
			if tracerProvider != nil {
				_ = tracerProvider.Shutdown(ctx)
			}

			return Telemetry{Shutdown: noopShutdown}, err
		}

		otel.SetMeterProvider(meterProvider)

		providers = append(providers, meterProvider)
		metricsHandler = handler
	}

	if tracerProvider != nil {
		otel.SetTracerProvider(tracerProvider)

		// W3C TraceContext + Baggage: needed so the trace header coming from the
		// client can be continued. Without it every service produces its own
		// disconnected trace and distributed tracing joins nothing.
		//
		// It is installed with the TRACER and not unconditionally: with no
		// tracer provider there is no trace to continue, and a propagator on
		// its own would only move a header between two no-ops.
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
	}

	log.InfoContext(ctx, "telemetry is set up",
		"endpoint", opts.Endpoint,
		"insecure", opts.Insecure,
		"sample_ratio", opts.SampleRatio,
		"traces", tracerProvider != nil,
		"metrics", metricsHandler != nil)

	return Telemetry{
		Shutdown: func(ctx context.Context) error {
			return runShutdown(ctx, shutdownTimeout, providers...)
		},
		MetricsHandler: metricsHandler,
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
//
// The provider list is VARIADIC and may hold one entry or none: since ADR 0046
// each signal is built only when it was asked for, so "close both" is no longer
// the only shape a shutdown can take.
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
//
// There is no metric counterpart any more: since ADR 0046 the metric side has
// no exporter that dials anywhere, so nothing on that side has an address to
// spell one way or the other.
func traceEndpoint(endpoint string) otlptracegrpc.Option {
	if endpointHasScheme(endpoint) {
		return otlptracegrpc.WithEndpointURL(endpoint)
	}

	return otlptracegrpc.WithEndpoint(endpoint)
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

// newMeterProvider builds the meter provider with a PULL reader, and the
// handler that serves what it holds.
//
// # Why the reader pulls (ADR 0046)
//
// The reader used to be periodic and pushed to the same OTLP address the traces
// use, and the only collector this repository ships REFUSES metrics — so what
// an installation actually got was one "failed to upload metrics" line per
// export interval, forever, and no number anywhere. A pull reader turns that
// around: it opens no outbound connection, it needs no collector between a
// running shop and the question "how many requests are in flight right now",
// and an interval whose export would have failed is no longer a hole in the
// record, because the current value simply waits until somebody asks.
//
// # Why a registry of its own
//
// Two reasons, and the second is the load-bearing one. First, a private
// registry yields EXACTLY the instruments this process created — the two in
// core/http — while the client library's default registry would add its process
// and Go runtime collectors as well; ADR 0046 measured the private one and
// picked neither on the merits, so the code reproduces what was measured.
// Second, prometheus.DefaultRegisterer is a package-level global and registering
// on it twice FAILS: a second [Setup] in one process — which is what a test
// does — would return "duplicate metrics collector registration attempted" and
// make telemetry look broken for a reason that has nothing to do with the
// installation.
func newMeterProvider(
	res *resource.Resource, log *slog.Logger,
) (*sdkmetric.MeterProvider, http.Handler, error) {
	registry := promclient.NewRegistry()

	exporter, err := prometheus.New(prometheus.WithRegisterer(registry))
	if err != nil {
		return nil, nil, err
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(exporter),
	)

	// A mux rather than the bare handler, so the listener answers the scrape
	// path and NOTHING else. It is the same rule core/http.ProfilingHandler
	// follows: an unauthenticated listener has to serve exactly what it was
	// opened for, and a handler that answers every path is one mistake away
	// from serving something that wandered in from elsewhere in the process.
	mux := http.NewServeMux()
	mux.Handle(MetricsPath, promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		ErrorLog: scrapeErrorLog{log: log},
	}))

	return provider, mux, nil
}

// scrapeErrorLog carries a failed scrape into the application's own log.
//
// promhttp's default is to write NOTHING: a gather that fails answers the
// scraper with a 500 and leaves no trace on the server, so the operator sees a
// target go down and has nowhere to look. This repository has already paid for
// that exact shape on the push side, where a failing export was visible only in
// the collector's log and not in ours.
type scrapeErrorLog struct{ log *slog.Logger }

// Println satisfies promhttp.Logger.
func (l scrapeErrorLog) Println(v ...any) {
	l.log.Error("the metrics scrape could not be served", "error", fmt.Sprint(v...))
}
