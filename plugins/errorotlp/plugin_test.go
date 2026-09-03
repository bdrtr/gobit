package errorotlp_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	coreplugin "github.com/bdrtr/gobit/internal/core/plugin"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/plugins/errorotlp"
)

// collector is a stand-in for an OpenTelemetry collector that keeps what it was
// posted.
type collector struct {
	*httptest.Server

	mu       sync.Mutex
	headers  []http.Header
	contents []string
	status   int
}

// newCollector starts a collector answering 200.
func newCollector(t *testing.T) *collector {
	t.Helper()

	c := &collector{status: http.StatusOK}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		c.mu.Lock()
		c.headers = append(c.headers, r.Header.Clone())
		c.contents = append(c.contents, string(body))
		status := c.status
		c.mu.Unlock()

		w.WriteHeader(status)
	}))
	t.Cleanup(c.Close)

	return c
}

// endpoint is the logs URL pointing at this collector.
func (c *collector) endpoint() string { return c.URL + "/v1/logs" }

// bodies returns what was posted.
func (c *collector) bodies() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]string{}, c.contents...)
}

// waitFor blocks until n requests have arrived.
func (c *collector) waitFor(t *testing.T, n int) []string {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := c.bodies(); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%d requests were expected, %d arrived", n, len(c.bodies()))

	return nil
}

// install runs the plugin's Setup against a real container and returns the
// registered reporter.
func install(t *testing.T, settings map[string]string) (coreprovider.ErrorReporter, *container.Container) {
	t.Helper()

	c := container.New(slog.New(slog.DiscardHandler))
	host := coreplugin.NewHost(c, nil, nil, slog.New(slog.DiscardHandler), settings)

	require.NoError(t, errorotlp.New().Setup(context.Background(), host))

	reporter, err := container.Resolve[coreprovider.ErrorReporter](c, coreplugin.ErrorReporterName)
	require.NoError(t, err)
	// The cleanup's Close is given a DEADLINE, for the reason the Sentry
	// plugin's tests give: cleanups run last-registered first, so an unbounded
	// Close would wait on a handler nothing has released yet.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = reporter.Close(ctx)
	})

	return reporter, c
}

// logRecord digs the single record out of an OTLP logs request.
//
// The nesting is the protocol's: a request holds resourceLogs, each holds
// scopeLogs, each holds logRecords. Walking it in the test rather than
// asserting on a flattened copy is deliberate — a collector walks the same
// path, and a payload that lost a level would still match a flat assertion.
func logRecord(t *testing.T, body string) (record, resourceAttrs map[string]any) {
	t.Helper()

	var payload struct {
		ResourceLogs []struct {
			Resource struct {
				Attributes []map[string]any `json:"attributes"`
			} `json:"resource"`
			ScopeLogs []struct {
				Scope      map[string]any   `json:"scope"`
				LogRecords []map[string]any `json:"logRecords"`
			} `json:"scopeLogs"`
		} `json:"resourceLogs"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &payload))

	require.Len(t, payload.ResourceLogs, 1)
	require.Len(t, payload.ResourceLogs[0].ScopeLogs, 1)
	require.Len(t, payload.ResourceLogs[0].ScopeLogs[0].LogRecords, 1)
	assert.Equal(t, "gobit/errorreport", payload.ResourceLogs[0].ScopeLogs[0].Scope["name"])

	return payload.ResourceLogs[0].ScopeLogs[0].LogRecords[0],
		attrMap(payload.ResourceLogs[0].Resource.Attributes)
}

// attrMap turns OTLP's list of key/value objects into a map.
func attrMap(list []map[string]any) map[string]any {
	out := make(map[string]any, len(list))
	for _, entry := range list {
		key, ok := entry["key"].(string)
		if !ok {
			continue
		}

		value, _ := entry["value"].(map[string]any)
		out[key] = value["stringValue"]
	}

	return out
}

// recordAttrs pulls the attributes off a log record.
func recordAttrs(t *testing.T, record map[string]any) map[string]any {
	t.Helper()

	raw, ok := record["attributes"].([]any)
	require.True(t, ok, "a record carries its attributes as a list")

	list := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		item, ok := entry.(map[string]any)
		require.True(t, ok)
		list = append(list, item)
	}

	return attrMap(list)
}

// event is a failure with every field the core can fill.
func event() coreprovider.ErrorEvent {
	return coreprovider.ErrorEvent{
		Time:      time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC),
		Message:   "request ended with a server error",
		Code:      "product_not_found",
		Kind:      "not_found",
		Detail:    "the product could not be read",
		RequestID: "req_1",
		TraceID:   "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:    "00f067aa0ba902b7",
		Attrs:     map[string]string{"method": "GET"},
		Redacted:  []string{"user_id"},
	}
}

// TestTheReporterRegistersUnderTheCoresName proves the plugin fills the slot
// the core reads.
func TestTheReporterRegistersUnderTheCoresName(t *testing.T) {
	t.Parallel()

	otel := newCollector(t)

	reporter, c := install(t, map[string]string{"OTLP_LOGS_ENDPOINT": otel.endpoint()})

	assert.Equal(t, "otlp", reporter.ID())
	assert.True(t, c.Has(coreplugin.ErrorReporterName))
}

// TestWithoutAnEndpointTheProcessDoesNotStart proves the plugin refuses to be a
// no-op, exactly as the Sentry one does.
//
// An installation that believes it has error reporting and does not is worse off
// than one that knows it has none: the second looks at its logs.
func TestWithoutAnEndpointTheProcessDoesNotStart(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	host := coreplugin.NewHost(c, nil, nil, slog.New(slog.DiscardHandler), nil)

	err := errorotlp.New().Setup(context.Background(), host)

	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "error: %v", err)
	assert.False(t, c.Has(coreplugin.ErrorReporterName), "nothing may be registered")
}

// TestAMalformedEndpointIsRefusedAtStartup keeps a nearly-right URL from
// becoming silence.
func TestAMalformedEndpointIsRefusedAtStartup(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"no scheme":       "collector:4318/v1/logs",
		"a wrong scheme":  "grpc://collector:4318",
		"no host":         "http:///v1/logs",
		"whitespace only": "   ",
	}

	for name, endpoint := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := container.New(slog.New(slog.DiscardHandler))
			host := coreplugin.NewHost(c, nil, nil, slog.New(slog.DiscardHandler),
				map[string]string{"OTLP_LOGS_ENDPOINT": endpoint})

			err := errorotlp.New().Setup(context.Background(), host)

			require.Error(t, err)
			assert.True(t, coreerrors.IsInvalid(err), "error: %v", err)
		})
	}
}

// TestAMalformedHeaderIsRefusedAtStartup keeps a mistyped API key from turning
// every report into a 401.
func TestAMalformedHeaderIsRefusedAtStartup(t *testing.T) {
	t.Parallel()

	otel := newCollector(t)
	c := container.New(slog.New(slog.DiscardHandler))
	host := coreplugin.NewHost(c, nil, nil, slog.New(slog.DiscardHandler), map[string]string{
		"OTLP_LOGS_ENDPOINT": otel.endpoint(),
		"OTLP_LOGS_HEADERS":  "api-key",
	})

	err := errorotlp.New().Setup(context.Background(), host)

	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "error: %v", err)
}

// TestTheRecordIsWhatOTLPExpects is the shape test: it walks the payload the
// way a collector does.
func TestTheRecordIsWhatOTLPExpects(t *testing.T) {
	t.Parallel()

	otel := newCollector(t)
	reporter, _ := install(t, map[string]string{
		"OTLP_LOGS_ENDPOINT":   otel.endpoint(),
		"SERVICE_NAME":         "gobit-store",
		"APP_ENV":              "production",
		"OTLP_SERVICE_VERSION": "v0.8.0",
	})

	reporter.Report(context.Background(), event())

	record, resourceAttrs := logRecord(t, otel.waitFor(t, 1)[0])
	attrs := recordAttrs(t, record)

	assert.Equal(t, "gobit-store", resourceAttrs["service.name"])
	assert.Equal(t, "production", resourceAttrs["deployment.environment"])
	assert.Equal(t, "v0.8.0", resourceAttrs["service.version"])

	assert.Equal(t, float64(17), record["severityNumber"], "17 is ERROR in the OTel model")
	assert.Equal(t, "ERROR", record["severityText"])
	assert.Equal(t, "1788516000000000000", record["timeUnixNano"],
		"the time is nanoseconds since the epoch, as a STRING: OTLP's JSON mapping "+
			"encodes 64-bit integers as strings and a number here would lose precision")

	body, ok := record["body"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "request ended with a server error", body["stringValue"],
		"the body is the log literal, so the same failure produces the same body")

	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", record["traceId"],
		"the trace binding is the record's own field, not an attribute")
	assert.Equal(t, "00f067aa0ba902b7", record["spanId"])

	assert.Equal(t, "product_not_found", attrs["error.code"])
	assert.Equal(t, "not_found", attrs["error.kind"])
	assert.Equal(t, "the product could not be read", attrs["error.detail"])
	assert.Equal(t, "req_1", attrs["request.id"])
	assert.Equal(t, "GET", attrs["method"])
	assert.Equal(t, "user_id", attrs["error.redacted"],
		"the names of what was dropped travel; a missing field would look like one never set")
}

// TestTheCodeAlsoTravelsAsTheGroupingConvention is the ADR 0014 question, asked
// of the model furthest from Sentry's.
//
// Sentry gets a fingerprint field and groups by it. The OTel log model has no
// grouping at all, so the only thing a collector's error view can group by is
// the SEMANTIC CONVENTION exception.type. gobit's Code is what goes there — the
// same value ADR 0014 calls the fingerprint — which is the answer to the
// question the ADR left open: a code is enough for a collector that groups by
// type, and no stack is needed to get there.
func TestTheCodeAlsoTravelsAsTheGroupingConvention(t *testing.T) {
	t.Parallel()

	otel := newCollector(t)
	reporter, _ := install(t, map[string]string{"OTLP_LOGS_ENDPOINT": otel.endpoint()})

	reporter.Report(context.Background(), event())

	record, _ := logRecord(t, otel.waitFor(t, 1)[0])
	attrs := recordAttrs(t, record)

	assert.Equal(t, "product_not_found", attrs["exception.type"],
		"a collector that knows nothing about gobit still groups by the code")
	assert.Equal(t, attrs["error.code"], attrs["exception.type"],
		"the two names carry the same value; one is gobit's, one is the convention")
}

// TestAnOrdinaryErrorCarriesNoStack pins what ADR 0014's decision 3 costs, in
// the place it is visible.
//
// A collector built around stack frames has nothing to show for an ordinary
// error, and that is the deliberate outcome: the frames that produced the error
// are gone by the time it is logged, and a fabricated stack pointing at the
// logging call looks like an answer while being none.
func TestAnOrdinaryErrorCarriesNoStack(t *testing.T) {
	t.Parallel()

	otel := newCollector(t)
	reporter, _ := install(t, map[string]string{"OTLP_LOGS_ENDPOINT": otel.endpoint()})

	reporter.Report(context.Background(), event())

	record, _ := logRecord(t, otel.waitFor(t, 1)[0])
	attrs := recordAttrs(t, record)

	assert.NotContains(t, attrs, "exception.stacktrace")
}

// TestAPanicCarriesItsStack draws the other side: the one case where a stack
// exists is the one where it is sent.
func TestAPanicCarriesItsStack(t *testing.T) {
	t.Parallel()

	otel := newCollector(t)
	reporter, _ := install(t, map[string]string{"OTLP_LOGS_ENDPOINT": otel.endpoint()})

	panicked := event()
	panicked.Code = "handler_panicked"
	panicked.Stack = "goroutine 1 [running]:\nmain.main()"
	reporter.Report(context.Background(), panicked)

	record, _ := logRecord(t, otel.waitFor(t, 1)[0])
	attrs := recordAttrs(t, record)

	assert.Contains(t, attrs["exception.stacktrace"], "goroutine 1")
}

// TestTheHeadersReachTheCollector proves a hosted collector's API key is sent.
func TestTheHeadersReachTheCollector(t *testing.T) {
	t.Parallel()

	otel := newCollector(t)
	reporter, _ := install(t, map[string]string{
		"OTLP_LOGS_ENDPOINT": otel.endpoint(),
		"OTLP_LOGS_HEADERS":  "api-key=secret, x-tenant = acme",
	})

	reporter.Report(context.Background(), event())
	otel.waitFor(t, 1)

	otel.mu.Lock()
	defer otel.mu.Unlock()
	require.NotEmpty(t, otel.headers)
	assert.Equal(t, "secret", otel.headers[0].Get("api-key"))
	assert.Equal(t, "acme", otel.headers[0].Get("x-tenant"), "the pairs are trimmed")
	assert.Equal(t, "application/json", otel.headers[0].Get("Content-Type"))
}

// TestReportingIsAsynchronous proves Report does not wait for the collector.
//
// The caller is inside a log handler, inside a request. A synchronous POST would
// add the collector's latency to every failing request and its outage to ours.
func TestReportingIsAsynchronous(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	var served atomic.Int64
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() {
		close(release)
		slow.Close()
	})

	reporter, _ := install(t, map[string]string{"OTLP_LOGS_ENDPOINT": slow.URL + "/v1/logs"})

	start := time.Now()
	reporter.Report(context.Background(), event())
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 100*time.Millisecond, "Report must not wait for the collector")
}

// TestARefusedReportIsNotRetried pins ADR 0014's decision 11.
func TestARefusedReportIsNotRetried(t *testing.T) {
	t.Parallel()

	otel := newCollector(t)
	otel.mu.Lock()
	otel.status = http.StatusInternalServerError
	otel.mu.Unlock()

	reporter, _ := install(t, map[string]string{"OTLP_LOGS_ENDPOINT": otel.endpoint()})

	reporter.Report(context.Background(), event())
	otel.waitFor(t, 1)

	time.Sleep(150 * time.Millisecond)
	assert.Len(t, otel.bodies(), 1, "a refused report is dropped, not retried")
}

// TestCloseFlushesWhatIsQueued proves the one place a reporter may block.
//
// The reports of the failures that happened just before the process stopped are
// the ones most worth having, and they are exactly the ones an unflushed queue
// loses.
func TestCloseFlushesWhatIsQueued(t *testing.T) {
	t.Parallel()

	otel := newCollector(t)
	reporter, _ := install(t, map[string]string{"OTLP_LOGS_ENDPOINT": otel.endpoint()})

	for range 5 {
		reporter.Report(context.Background(), event())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, reporter.Close(ctx))

	assert.Len(t, otel.bodies(), 5, "everything queued has to be sent before Close returns")
}

// TestReportingAfterCloseIsSafe proves a late report is dropped rather than
// panicking on a closed channel.
func TestReportingAfterCloseIsSafe(t *testing.T) {
	t.Parallel()

	otel := newCollector(t)
	reporter, _ := install(t, map[string]string{"OTLP_LOGS_ENDPOINT": otel.endpoint()})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, reporter.Close(ctx))

	assert.NotPanics(t, func() { reporter.Report(context.Background(), event()) })
	assert.NoError(t, reporter.Close(ctx), "closing twice is not an error")
}

// TestAFullQueueDropsAndSaysHowMany proves a burst that overflowed does not look
// like a burst that did not happen.
func TestAFullQueueDropsAndSaysHowMany(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	var served atomic.Int64
	blocked := newCollector(t)
	blocked.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if served.Add(1) == 1 {
			<-release
		}

		body, _ := io.ReadAll(r.Body)
		blocked.mu.Lock()
		blocked.contents = append(blocked.contents, string(body))
		blocked.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	t.Cleanup(func() { close(release) })

	reporter, _ := install(t, map[string]string{"OTLP_LOGS_ENDPOINT": blocked.endpoint()})

	// One event is in the sender's hands and the queue takes 256 more; the rest
	// have nowhere to go.
	for range queueOverflow {
		reporter.Report(context.Background(), event())
	}

	release <- struct{}{}

	bodies := blocked.waitFor(t, 2)
	var dropped string
	for _, body := range bodies {
		record, _ := logRecord(t, body)
		if value, ok := recordAttrs(t, record)["error.dropped_by_full_queue"].(string); ok {
			dropped = value

			break
		}
	}

	assert.NotEmpty(t, dropped, "the count of what a full queue refused has to ride the next report")
}

// queueOverflow is more events than the queue plus the sender can hold.
const queueOverflow = 300
