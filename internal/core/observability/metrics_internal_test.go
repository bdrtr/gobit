package observability

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// httpScope is the instrumentation scope core/http records under.
//
// It is repeated here rather than imported because the name is unexported
// there, and the value is not what these tests are about: what matters is that
// ONE scope's two instruments come out of the scrape endpoint, whatever the
// scope is called.
const httpScope = "github.com/bdrtr/gobit/core/http"

// quietLogger keeps the setup lines out of the test output.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// restoreGlobals puts OTel's no-op providers back after a test that installed
// real ones.
//
// The global providers are process-wide and [Setup] is the function under test,
// so without this a test that turns telemetry on leaves it on for every test
// that runs after it in this binary.
func restoreGlobals(t *testing.T) {
	t.Helper()

	t.Cleanup(func() {
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
		otel.SetTracerProvider(tracenoop.NewTracerProvider())
	})
}

// recordOneOfEach creates the two instruments core/http creates and records one
// measurement into each.
//
// The instruments are taken from the GLOBAL meter on purpose. That is the only
// way core/http can reach a provider — it is handed no provider and calls
// otel.Meter — so a Setup that built a meter provider and forgot to install it
// globally would leave the scrape endpoint permanently empty while every
// return value still looked right.
func recordOneOfEach(ctx context.Context, t *testing.T) {
	t.Helper()

	meter := otel.Meter(httpScope)

	duration, err := meter.Float64Histogram("http.server.request.duration",
		metric.WithUnit("s"),
		metric.WithDescription("the duration of HTTP requests in seconds"))
	require.NoError(t, err)

	active, err := meter.Int64UpDownCounter("http.server.active_requests",
		metric.WithDescription("the HTTP requests being handled right now"))
	require.NoError(t, err)

	active.Add(ctx, 1)
	duration.Record(ctx, 0.012)
}

// scrape asks the handler for the metrics path and returns the status and body.
func scrape(t *testing.T, handler http.Handler, path string) (status int, body string) {
	t.Helper()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))

	return rec.Code, rec.Body.String()
}

// TestEachSignalIsBuiltOnlyForTheSwitchThatAskedForIt pins the widening of
// Setup's early return (ADR 0046).
//
// The behavior it replaces was a single address deciding both signals: an empty
// OTEL_EXPORTER_OTLP_ENDPOINT returned before EITHER provider was built, so a
// stock installation had no meter provider and the two instruments in core/http
// recorded into no-ops. That was invisible — nothing failed, nothing logged,
// and two documents described the opposite — which is why the answer now lives
// in a test rather than in a sentence.
//
// The metrics switch is checked through the HANDLER because that is the whole
// observable difference: a handler means an installation can be scraped, nil
// means internal/app opens no listener at all.
func TestEachSignalIsBuiltOnlyForTheSwitchThatAskedForIt(t *testing.T) {
	tests := map[string]struct {
		endpoint    string
		metrics     bool
		wantScrape  bool
		description string
	}{
		"neither signal asked for": {
			endpoint: "", metrics: false, wantScrape: false,
			description: "the stock install: nothing is built and nothing is served",
		},
		"metrics alone": {
			endpoint: "", metrics: true, wantScrape: true,
			description: "no collector anywhere, and the shop can still answer for itself",
		},
		"traces alone": {
			endpoint: "localhost:4317", metrics: false, wantScrape: false,
			description: "the OTLP address no longer drags the metric signal along with it",
		},
		"both signals": {
			endpoint: "localhost:4317", metrics: true, wantScrape: true,
			description: "two switches, two doors, and neither one touches the other",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			restoreGlobals(t)

			ctx := context.Background()

			telemetry, err := Setup(ctx, Options{
				Endpoint:    tt.endpoint,
				Insecure:    true,
				ServiceName: "gobit-test",
				Metrics:     tt.metrics,
				Logger:      quietLogger(),
			})
			require.NoError(t, err)

			// Always callable, even with everything off; a caller must never
			// have to write a conditional shutdown path.
			require.NotNil(t, telemetry.Shutdown, "the shutdown must never be nil")
			t.Cleanup(func() { _ = telemetry.Shutdown(context.Background()) })

			if !tt.wantScrape {
				assert.Nil(t, telemetry.MetricsHandler,
					"no metrics were asked for, so there must be nothing to serve: %s", tt.description)

				return
			}

			require.NotNil(t, telemetry.MetricsHandler, tt.description)

			recordOneOfEach(ctx, t)

			code, body := scrape(t, telemetry.MetricsHandler, MetricsPath)
			assert.Equal(t, http.StatusOK, code, "the scrape must be answered: %s", tt.description)
			assert.Contains(t, body, "http_server_active_requests",
				"the in-flight instrument must reach the scrape output")
		})
	}
}

// TestTheScrapeOutputCarriesTheNamesADR0046Measured pins the exposed names,
// types and suffix.
//
// These are what a dashboard is written against, and two of them are surprises
// worth catching if they ever change. The dots of the OTLP name become
// underscores, and the duration instrument gains a _seconds SUFFIX that the
// OTLP name does not carry — the exporter reads the unit the instrument
// declares. Somebody who wrote a query against the un-suffixed name would get
// silence rather than an error.
//
// The provider is built directly instead of through [Setup] so the test does
// not depend on the global providers, which OTel installs once per process.
func TestTheScrapeOutputCarriesTheNamesADR0046Measured(t *testing.T) {
	ctx := context.Background()

	res, err := newResource(ctx, Options{ServiceName: "gobit-test", ServiceVersion: "test"})
	require.NoError(t, err)

	provider, handler, err := newMeterProvider(res, quietLogger())
	require.NoError(t, err)

	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	meter := provider.Meter(httpScope)

	duration, err := meter.Float64Histogram("http.server.request.duration",
		metric.WithUnit("s"),
		metric.WithDescription("the duration of HTTP requests in seconds"))
	require.NoError(t, err)

	active, err := meter.Int64UpDownCounter("http.server.active_requests",
		metric.WithDescription("the HTTP requests being handled right now"))
	require.NoError(t, err)

	active.Add(ctx, 1)
	duration.Record(ctx, 0.012)

	code, body := scrape(t, handler, MetricsPath)
	require.Equal(t, http.StatusOK, code, body)

	for _, want := range []string{
		// An up/down counter is a GAUGE on this side: it goes down as well as
		// up, so a counter type would make every rate() query wrong.
		"# TYPE http_server_active_requests gauge",
		"# TYPE http_server_request_duration_seconds histogram",
		"http_server_request_duration_seconds_bucket",
		"http_server_request_duration_seconds_sum",
		"http_server_request_duration_seconds_count",
	} {
		assert.Contains(t, body, want)
	}
}

// TestTheMetricsHandlerServesNothingElse keeps the listener from becoming a
// general-purpose surface.
//
// The listener is unauthenticated and MAY be bound off loopback — a scrape
// comes from another host by definition, so the loopback refusal pprof carries
// could not be borrowed with the rest of its shape (ADR 0046). What is left
// guarding it is the address the operator picks and the fact that there is
// exactly one thing to ask for.
func TestTheMetricsHandlerServesNothingElse(t *testing.T) {
	restoreGlobals(t)

	telemetry, err := Setup(context.Background(), Options{
		ServiceName: "gobit-test",
		Metrics:     true,
		Logger:      quietLogger(),
	})
	require.NoError(t, err)
	require.NotNil(t, telemetry.MetricsHandler)

	t.Cleanup(func() { _ = telemetry.Shutdown(context.Background()) })

	for _, path := range []string{"/", "/debug/pprof/", "/store/v1/products", "/metrics/"} {
		code, _ := scrape(t, telemetry.MetricsHandler, path)
		assert.Equal(t, http.StatusNotFound, code, path)
	}
}
