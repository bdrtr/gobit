package http

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// tracerName is the instrumentation name of the spans this package produces.
const tracerName = "github.com/bdrtr/gobit/internal/core/http"

// unknownRoute is the name used when no route pattern could be matched.
//
// Using the raw request path is TEMPTING and ruinous: /store/v1/products/prod_01
// produces a separate span name and a separate metric series for every id. With
// a few thousand products the metric store grows to millions of series and
// becomes unqueryable. Folding an unrecognized path into one bucket is better
// patlatmaktan iyidir.
const unknownRoute = "unknown"

// Telemetry opens a span per request and records duration/count metrics.
//
// serviceName is written onto the produced spans and HTTP metrics as the
// service.name attribute. Leaving the name to the OTel Resource alone is
// TEMPTING — that is where a service identity belongs — but this middleware
// works with the GLOBAL provider and [NewRouter] does not require its caller to
// have set up observability.Setup; if another embedder built the provider the
// Resource may carry no service name and which service a span came from is
// lost. The parameter would then be a "should it be plugged in" switch: the
// operator changes the value and nothing changes in the tracing.
//
// The attribute is safe for cardinality: serviceName is ONE value for the life
// of the process, it puts the same value on every series and multiplies no
// series. A value that changes per request (an id, a raw path) must NEVER go here.
//
// An empty name is not written as an attribute: an empty service.name is worse
// than not reporting a name at all — it opens a nameless series that looks like
// a real service on a dashboard.
//
// With tracing not set up, OTel's global no-op providers are in play and the
// middleware costs nothing measurable; that is why there is no conditional
// needed.
//
// In the chain it has to run AFTER [RequestID] and BEFORE the route match: the
// route pattern used as the span name is only known once the handler has run,
// so the name is updated when next returns.
func Telemetry(serviceName string) func(http.Handler) http.Handler {
	tracer := otel.Tracer(tracerName)
	meter := otel.Meter(tracerName)

	// Turn the service name into an attribute once: the value is fixed for the
	// life of the process and rebuilding it per request would be a pointless
	var constantAttrs []attribute.KeyValue
	if serviceName != "" {
		constantAttrs = append(constantAttrs, semconv.ServiceName(serviceName))
	}

	// The increment and the decrement of the in-flight counter MUST use the SAME
	// attribute set; if they drift apart two series appear, one stuck at +1 and
	// the other at -1 forever, and the "how many requests are in flight"
	// dashboard never returns to zero. Building the option once here makes that
	// garantiler.
	constantMeasurement := metric.WithAttributes(constantAttrs...)

	// Build the metric instruments once. An error can only come from an invalid
	// instrument name; the instruments then stay nil and recording is skipped
	// silently — a problem in the tracing setup must not drop the request path
	duration, err := meter.Float64Histogram("http.server.request.duration",
		metric.WithUnit("s"),
		metric.WithDescription("the duration of HTTP requests in seconds"))
	if err != nil {
		slog.Default().Warn("the request duration metric could not be built", "error", err)
	}

	aktif, err := meter.Int64UpDownCounter("http.server.active_requests",
		metric.WithDescription("the HTTP requests being handled right now"))
	if err != nil {
		slog.Default().Warn("the in-flight request metric could not be built", "error", err)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Continue the trace context the client sent; without one a new trace starts.
			ctx := otel.GetTextMapPropagator().Extract(
				r.Context(), propagation.HeaderCarrier(r.Header))

			// The fixed attributes are given when the span STARTS; adding them
			// later with SetAttributes would not do, because the sampler makes its
			// decision from the starting attributes alone and a setup that samples
			// by service name would not see it.
			spanAttrs := make([]attribute.KeyValue, 0, len(constantAttrs)+3)
			spanAttrs = append(spanAttrs, constantAttrs...)
			spanAttrs = append(spanAttrs,
				attribute.String("http.request.method", r.Method),
				attribute.String("url.path", r.URL.Path),
				attribute.String("server.address", r.Host),
			)

			ctx, span := tracer.Start(ctx, r.Method+" "+unknownRoute,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(spanAttrs...))
			defer span.End()

			if id := RequestIDFromContext(ctx); id != "" {
				span.SetAttributes(attribute.String("gobit.request_id", id))
			}

			r = r.WithContext(ctx)
			wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			started := time.Now()

			if aktif != nil {
				aktif.Add(ctx, 1, constantMeasurement)
				defer aktif.Add(ctx, -1, constantMeasurement)
			}

			next.ServeHTTP(wrapped, r)

			desen := routePattern(r)
			span.SetName(r.Method + " " + desen)
			span.SetAttributes(
				attribute.String("http.route", desen),
				attribute.Int("http.response.status_code", wrapped.status),
			)

			// A 5xx is the server's own failure and has to show as an error on the
			// trace. A 4xx is not marked: invalid data the client sent is not the
			// server's fault, and marking it would skew the error-rate graphs and
			// drown the real failures in noise.
			if wrapped.status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(wrapped.status))
			}

			if duration != nil {
				// The attributes are gathered into a single slice:
				// metric.WithAttributes REPLACES the set rather than adding to
				// it — with two separate options the second would silently drop
				measurementAttrs := make([]attribute.KeyValue, 0, len(constantAttrs)+3)
				measurementAttrs = append(measurementAttrs, constantAttrs...)
				measurementAttrs = append(measurementAttrs,
					attribute.String("http.request.method", r.Method),
					attribute.String("http.route", desen),
					attribute.Int("http.response.status_code", wrapped.status),
				)

				duration.Record(ctx, time.Since(started).Seconds(),
					metric.WithAttributes(measurementAttrs...))
			}
		})
	}
}

// routePattern returns the matched chi route pattern.
//
// The pattern is only filled once the router has matched, so it may only be
// called after the handler has run.
func routePattern(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return unknownRoute
	}

	if desen := rctx.RoutePattern(); desen != "" {
		return desen
	}

	return unknownRoute
}

// SpanFromContext returns the active span from the context.
//
// It is safe to call with tracing off: OTel returns a no-op span.
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// TraceIDFromContext returns the id of the active trace, or an empty string.
//
// It is meant for log records: binding an error log to its trace is the
// ucuz yolu budur.
func TraceIDFromContext(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}

	return sc.TraceID().String()
}
