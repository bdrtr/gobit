package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// setUpTracer sets up a tracer provider collecting spans in memory and swaps the
// global provider for it for the duration of the test.
//
// Because it changes global state these tests are NOT PARALLEL.
func setUpTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)

	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = tp.Shutdown(t.Context())
	})

	return recorder
}

// setUpMeter sets up a meter provider collecting metrics in memory and swaps the
// global provider for it for the duration of the test.
//
// It has to be called BEFORE the router: [corehttp.Telemetry] creates the metric
// instruments once while the middleware is being built and does not see a
// provider installed later.
//
// Because it changes global state these tests are NOT PARALLEL.
func setUpMeter(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()

	okuyucu := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(okuyucu))

	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)

	t.Cleanup(func() {
		otel.SetMeterProvider(previous)
		// t.Context() has already been canceled during cleanup; Shutdown needs a live
		// context to be able to finish the export.
		_ = mp.Shutdown(context.Background())
	})

	return okuyucu
}

// attributeOf reads one attribute off a span.
func attributeOf(t *testing.T, s sdktrace.ReadOnlySpan, key string) attribute.Value {
	t.Helper()

	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value
		}
	}

	t.Fatalf("the %q attribute was not found", key)

	return attribute.Value{}
}

// hasAttribute says whether an attribute is present on the span.
//
// [attributeOf] fails the test on absence, so it cannot be used in "must not be
// there" assertions; this helper closes that gap.
func hasAttribute(s sdktrace.ReadOnlySpan, key string) bool {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return true
		}
	}

	return false
}

// metricData returns the data of the metric with the given name out of the
// collected metrics; it fails the test if the metric is not there.
func metricData(t *testing.T, rm *metricdata.ResourceMetrics, name string) metricdata.Aggregation {
	t.Helper()

	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == name {
				return m.Data
			}
		}
	}

	t.Fatalf("the %q metric was not found", name)

	return nil
}

// histogramPoints returns the data points of the histogram metric with the given name.
func histogramPoints(
	t *testing.T, rm *metricdata.ResourceMetrics, name string,
) []metricdata.HistogramDataPoint[float64] {
	t.Helper()

	h, ok := metricData(t, rm, name).(metricdata.Histogram[float64])
	require.True(t, ok, "%q has to be a histogram", name)

	return h.DataPoints
}

// sumPoints returns the data points of the counter metric with the given name.
func sumPoints(
	t *testing.T, rm *metricdata.ResourceMetrics, name string,
) []metricdata.DataPoint[int64] {
	t.Helper()

	s, ok := metricData(t, rm, name).(metricdata.Sum[int64])
	require.True(t, ok, "%q has to be a counter", name)

	return s.DataPoints
}

// setUpRouter builds a chi router with the telemetry middleware installed.
func setUpRouter(t *testing.T) chi.Router {
	t.Helper()

	return setUpRouterNamed(t, "gobit-test")
}

// setUpRouterNamed builds a chi router with the telemetry middleware installed
// under the given service name.
func setUpRouterNamed(t *testing.T, serviceName string) chi.Router {
	t.Helper()

	r := chi.NewRouter()
	r.Use(corehttp.Telemetry(serviceName))
	r.Get("/store/v1/products/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/store/v1/patlat", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	r.Get("/store/v1/invalid", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	})

	return r
}

// TestTelemetryTheSpanNameCarriesNoIdentity verifies that the span name uses the
// route pattern, NOT the raw path.
//
// What this test guards is a cardinality explosion: were the raw path used, every
// product id would produce its own span name and its own metric series, and with
// a few thousand products the metric store would become unqueryable.
func TestTelemetryTheSpanNameCarriesNoIdentity(t *testing.T) {
	recorder := setUpTracer(t)
	r := setUpRouter(t)

	for _, id := range []string{"prod_01", "prod_02", "prod_03"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
			"/store/v1/products/"+id, http.NoBody))
		require.Equal(t, http.StatusOK, w.Code)
	}

	spans := recorder.Ended()
	require.Len(t, spans, 3)

	names := map[string]int{}
	for _, s := range spans {
		names[s.Name()]++
		assert.NotContains(t, s.Name(), "prod_", "the span name must not carry an identity")
	}

	assert.Equal(t, map[string]int{"GET /store/v1/products/{id}": 3}, names,
		"three different ids have to fall into a SINGLE span name")
}

// TestTelemetryTheRawPathStaysInAnAttribute verifies that taking the id out of
// the span name does NOT LOSE the information.
//
// The raw path stays as an attribute: attributes do not carry cardinality into
// the metric series, while looking at a single span still shows which record was
// asked for.
func TestTelemetryTheRawPathStaysInAnAttribute(t *testing.T) {
	recorder := setUpTracer(t)
	r := setUpRouter(t)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/store/v1/products/prod_42", http.NoBody))

	spans := recorder.Ended()
	require.Len(t, spans, 1)

	assert.Equal(t, "/store/v1/products/prod_42",
		attributeOf(t, spans[0], "url.path").AsString())
	assert.Equal(t, "/store/v1/products/{id}",
		attributeOf(t, spans[0], "http.route").AsString())
}

// TestTelemetryAServerErrorMarksTheSpan verifies that a 5xx shows up as an error
// on the span.
func TestTelemetryAServerErrorMarksTheSpan(t *testing.T) {
	recorder := setUpTracer(t)
	r := setUpRouter(t)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/store/v1/patlat", http.NoBody))
	require.Equal(t, http.StatusInternalServerError, w.Code)

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, codes.Error, spans[0].Status().Code)
	assert.EqualValues(t, http.StatusInternalServerError,
		attributeOf(t, spans[0], "http.response.status_code").AsInt64())
}

// TestTelemetryAClientErrorDoesNotMarkTheSpan verifies that a 4xx does NOT COUNT
// as an error.
//
// Invalid data sent by the client is not the server's fault; marking it as an
// error would mislead the error rate charts and drown real faults in noise.
func TestTelemetryAClientErrorDoesNotMarkTheSpan(t *testing.T) {
	recorder := setUpTracer(t)
	r := setUpRouter(t)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/store/v1/invalid", http.NoBody))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, codes.Unset, spans[0].Status().Code,
		"a 4xx must not be marked as a server error")
}

// TestTelemetryAnUnmatchedPathCollectsInASingleBucket verifies that 404s do not
// blow up cardinality.
//
// This is the most critical case: if every random path a crawler or a bot tries
// produced its own span name, a single attacker would be enough to fill the
// metric store.
func TestTelemetryAnUnmatchedPathCollectsInASingleBucket(t *testing.T) {
	recorder := setUpTracer(t)
	r := setUpRouter(t)

	for _, path := range []string{"/rastgele/1", "/rastgele/2", "/bambaska"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, http.NoBody))
		require.Equal(t, http.StatusNotFound, w.Code)
	}

	names := map[string]int{}
	for _, s := range recorder.Ended() {
		names[s.Name()]++
	}

	assert.Len(t, names, 1, "unmatched paths have to collect into a single span name")
}

// TestTelemetryAnIncomingTraceContextIsContinued verifies that the W3C
// traceparent header is followed.
//
// Without continuing it every service produces its own broken trace and
// distributed tracing cannot show any request end to end.
func TestTelemetryAnIncomingTraceContextIsContinued(t *testing.T) {
	recorder := setUpTracer(t)

	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagator())

	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })

	r := setUpRouter(t)

	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"

	req := httptest.NewRequest(http.MethodGet, "/store/v1/products/p1", http.NoBody)
	req.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")

	r.ServeHTTP(httptest.NewRecorder(), req)

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, traceID, spans[0].SpanContext().TraceID().String(),
		"the incoming trace id has to be continued")
	assert.True(t, spans[0].Parent().IsValid(), "the span has to have a parent")
}

// TestTelemetryTheServiceNameIsWrittenToTheSpan verifies that the service name
// given to [corehttp.Telemetry] lands in the span attributes.
//
// The parameter was at one time NOT USED at all in the body, while the
// documentation said the name was reported in tracing. That was the silent trap:
// the operator would change RouterOptions.TelemetryService and nothing in tracing
// would change. This test keeps that link alive.
func TestTelemetryTheServiceNameIsWrittenToTheSpan(t *testing.T) {
	recorder := setUpTracer(t)
	r := setUpRouterNamed(t, "gobit-magaza")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/store/v1/products/prod_1", http.NoBody))
	require.Equal(t, http.StatusOK, w.Code)

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, "gobit-magaza",
		attributeOf(t, spans[0], string(semconv.ServiceNameKey)).AsString())
}

// TestTelemetryAnEmptyServiceNameWritesNoAttribute verifies that an empty name
// does not write an empty service.name onto the span.
//
// An empty attribute is worse than not reporting the name at all: it opens a
// nameless series on the dashboards that looks like a real service, and the
// measurements of two different installations pile up in the same bucket.
func TestTelemetryAnEmptyServiceNameWritesNoAttribute(t *testing.T) {
	recorder := setUpTracer(t)
	r := setUpRouterNamed(t, "")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/store/v1/products/prod_1", http.NoBody))
	require.Equal(t, http.StatusOK, w.Code)

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	assert.False(t, hasAttribute(spans[0], string(semconv.ServiceNameKey)),
		"an empty service name must not be written as an attribute")
	assert.Equal(t, "/store/v1/products/{id}",
		attributeOf(t, spans[0], "http.route").AsString(),
		"the other attributes have to be written even without a service name")
}

// TestTelemetryTheServiceNameIsWrittenToTheMetricWithoutMultiplyingSeries
// verifies that the service name is among the attributes of the duration metric
// but does NOT MULTIPLY the number of series.
//
// This is the proof of cardinality safety: because the name is constant over the
// process lifetime, three requests still fall into a SINGLE series. Had a value
// that changes per request been added, this assertion would break at once.
func TestTelemetryTheServiceNameIsWrittenToTheMetricWithoutMultiplyingSeries(t *testing.T) {
	okuyucu := setUpMeter(t)
	r := setUpRouterNamed(t, "gobit-magaza")

	for _, id := range []string{"prod_01", "prod_02", "prod_03"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
			"/store/v1/products/"+id, http.NoBody))
		require.Equal(t, http.StatusOK, w.Code)
	}

	var rm metricdata.ResourceMetrics
	require.NoError(t, okuyucu.Collect(t.Context(), &rm))

	points := histogramPoints(t, &rm, "http.server.request.duration")
	require.Len(t, points, 1, "a constant service name must not multiply the number of series")
	assert.EqualValues(t, 3, points[0].Count)

	value, ok := points[0].Attributes.Value(semconv.ServiceNameKey)
	require.True(t, ok, "service.name has to be on the duration metric")
	assert.Equal(t, "gobit-magaza", value.AsString())
}

// TestTelemetryTheInFlightCounterReturnsToZero verifies that the counter goes
// back to zero once the requests are over.
//
// A real trap was born after the service name attribute was added: had the
// increment and the decrement used different attribute sets, two separate series
// would form, one stuck permanently at +3 and the other at -3, and the "how many
// requests are in flight" dashboard would never come back to the truth.
func TestTelemetryTheInFlightCounterReturnsToZero(t *testing.T) {
	okuyucu := setUpMeter(t)
	r := setUpRouterNamed(t, "gobit-magaza")

	for range 3 {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
			"/store/v1/products/prod_1", http.NoBody))
		require.Equal(t, http.StatusOK, w.Code)
	}

	var rm metricdata.ResourceMetrics
	require.NoError(t, okuyucu.Collect(t.Context(), &rm))

	points := sumPoints(t, &rm, "http.server.active_requests")
	require.Len(t, points, 1, "the increment and the decrement have to collect into a SINGLE series")
	assert.EqualValues(t, 0, points[0].Value, "the counter of finished requests has to return to zero")

	value, ok := points[0].Attributes.Value(semconv.ServiceNameKey)
	require.True(t, ok, "service.name has to be on the in-flight request counter")
	assert.Equal(t, "gobit-magaza", value.AsString())
}

// propagator is the W3C TraceContext propagator used in the tests.
func propagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{})
}
