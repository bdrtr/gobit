package errorsentry_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errorreport"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/logger"
	coreplugin "github.com/bdrtr/gobit/internal/core/plugin"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/plugins/errorsentry"
)

// collector is a stand-in for Sentry that keeps what it was posted.
type collector struct {
	*httptest.Server

	mu       sync.Mutex
	auth     []string
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
		c.auth = append(c.auth, r.Header.Get("X-Sentry-Auth"))
		c.contents = append(c.contents, string(body))
		status := c.status
		c.mu.Unlock()

		w.WriteHeader(status)
	}))
	t.Cleanup(c.Close)

	return c
}

// dsn is the DSN pointing at this collector.
func (c *collector) dsn() string {
	return strings.Replace(c.URL, "://", "://publickey@", 1) + "/42"
}

// envelopes returns what was posted.
func (c *collector) envelopes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]string{}, c.contents...)
}

// waitFor blocks until n envelopes have arrived.
func (c *collector) waitFor(t *testing.T, n int) []string {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := c.envelopes(); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%d envelopes were expected, %d arrived", n, len(c.envelopes()))

	return nil
}

// install runs the plugin's Setup against a real container and returns the
// registered reporter.
func install(t *testing.T, settings map[string]string) (coreprovider.ErrorReporter, *container.Container) {
	t.Helper()

	c := container.New(slog.New(slog.DiscardHandler))
	host := coreplugin.NewHost(c, nil, nil, slog.New(slog.DiscardHandler), settings)

	require.NoError(t, errorsentry.New().Setup(context.Background(), host))

	reporter, err := container.Resolve[coreprovider.ErrorReporter](c, coreplugin.ErrorReporterName)
	require.NoError(t, err)
	// The cleanup's Close is given a DEADLINE. Cleanups run last-registered
	// first, so this one fires before a test's own "stop blocking the
	// collector" cleanup, and an unbounded Close would then wait on a handler
	// nothing has released yet.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = reporter.Close(ctx)
	})

	return reporter, c
}

// payloadOf pulls the event out of an envelope's third line.
func payloadOf(t *testing.T, envelope string) map[string]any {
	t.Helper()

	lines := strings.Split(strings.TrimRight(envelope, "\n"), "\n")
	require.Len(t, lines, 3, "an envelope is a header, an item header and the item")

	var header, item, payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &header))
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &item))
	require.NoError(t, json.Unmarshal([]byte(lines[2]), &payload))

	assert.Equal(t, "event", item["type"])
	assert.Equal(t, float64(len(lines[2])), item["length"],
		"the item header's length must match the item, or the collector rejects the envelope")
	assert.Equal(t, header["event_id"], payload["event_id"])
	assert.Len(t, header["event_id"], 32, "sentry identifies an event by 32 hex characters")

	return payload
}

// TestTheReporterRegistersUnderTheCoresName proves the plugin fills the slot
// the core reads.
func TestTheReporterRegistersUnderTheCoresName(t *testing.T) {
	t.Parallel()

	sentry := newCollector(t)

	reporter, c := install(t, map[string]string{"SENTRY_DSN": sentry.dsn()})

	assert.Equal(t, "sentry", reporter.ID())
	assert.True(t, c.Has(coreplugin.ErrorReporterName))
}

// TestWithoutADSNTheProcessDoesNotStart proves the plugin refuses to be a
// no-op.
//
// An installation that believes it has error reporting and does not is worse
// off than one that knows it has none: the second looks at its logs.
func TestWithoutADSNTheProcessDoesNotStart(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	host := coreplugin.NewHost(c, nil, nil, slog.New(slog.DiscardHandler), nil)

	err := errorsentry.New().Setup(context.Background(), host)

	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err))
	assert.False(t, c.Has(coreplugin.ErrorReporterName))
}

// TestAMalformedDSNIsRefusedAtStartup is the same claim for the near misses.
//
// Each of these would otherwise produce a process that starts, logs that
// reporting is on, and posts every failure into a 404 for as long as it runs.
func TestAMalformedDSNIsRefusedAtStartup(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"no key":         "https://sentry.example/42",
		"no project":     "https://key@sentry.example",
		"no host":        "https://key@/42",
		"not a url":      "://",
		"wrong scheme":   "ftp://key@sentry.example/42",
		"a secret dsn":   "https://key:secret@sentry.example/42",
		"empty":          "   ",
		"trailing slash": "https://key@sentry.example/",
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := container.New(slog.New(slog.DiscardHandler))
			host := coreplugin.NewHost(c, nil, nil, slog.New(slog.DiscardHandler),
				map[string]string{"SENTRY_DSN": raw})

			err := errorsentry.New().Setup(context.Background(), host)

			require.Error(t, err, "a DSN that is not understood must not be guessed at")
			assert.True(t, coreerrors.IsInvalid(err))
		})
	}
}

// TestASelfHostedSubPathSurvivesTheDSN proves the path before the project id is
// kept.
//
// Dropping it would post every envelope to the wrong place on exactly the
// installations that are hardest to debug.
func TestASelfHostedSubPathSurvivesTheDSN(t *testing.T) {
	t.Parallel()

	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	reporter, _ := install(t, map[string]string{
		"SENTRY_DSN": strings.Replace(server.URL, "://", "://key@", 1) + "/sentry/42",
	})
	reporter.Report(context.Background(), coreprovider.ErrorEvent{Code: "boom"})
	require.NoError(t, reporter.Close(context.Background()))

	assert.Equal(t, "/sentry/api/42/envelope/", got)
}

// TestTheEnvelopeIsWhatSentryExpects walks the wire format.
func TestTheEnvelopeIsWhatSentryExpects(t *testing.T) {
	t.Parallel()

	sentry := newCollector(t)
	reporter, _ := install(t, map[string]string{
		"SENTRY_DSN":         sentry.dsn(),
		"SENTRY_ENVIRONMENT": "production",
		"SENTRY_RELEASE":     "v0.6.0",
	})

	reporter.Report(context.Background(), coreprovider.ErrorEvent{
		Time:      time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		Message:   "request ended with a server error",
		Code:      "db_unreachable",
		Kind:      "unavailable",
		Detail:    "the catalog is temporarily unavailable",
		RequestID: "req_01H",
		TraceID:   "abcd",
		SpanID:    "ef01",
		Attrs:     map[string]string{"path": "/store/v1/products", "status": "503"},
	})

	payload := payloadOf(t, sentry.waitFor(t, 1)[0])

	assert.Equal(t, "production", payload["environment"])
	assert.Equal(t, "v0.6.0", payload["release"])
	assert.Equal(t, "2026-09-03T12:00:00Z", payload["timestamp"])
	assert.Equal(t, []any{"db_unreachable"}, payload["fingerprint"],
		"the grouping key is the code, which does not move when a function is renamed")

	message, _ := payload["message"].(map[string]any)
	assert.Equal(t,
		"request ended with a server error — the catalog is temporarily unavailable",
		message["formatted"])

	tags, _ := payload["tags"].(map[string]any)
	assert.Equal(t, "db_unreachable", tags["code"])
	assert.Equal(t, "unavailable", tags["kind"])
	assert.Equal(t, "req_01H", tags["request_id"])

	extra, _ := payload["extra"].(map[string]any)
	assert.Equal(t, "/store/v1/products", extra["path"])
	assert.Equal(t, "503", extra["status"])

	contexts, _ := payload["contexts"].(map[string]any)
	trace, _ := contexts["trace"].(map[string]any)
	assert.Equal(t, "abcd", trace["trace_id"])
	assert.Equal(t, "ef01", trace["span_id"])
}

// TestTheAuthHeaderCarriesTheKey proves the collector can identify the project.
func TestTheAuthHeaderCarriesTheKey(t *testing.T) {
	t.Parallel()

	sentry := newCollector(t)
	reporter, _ := install(t, map[string]string{"SENTRY_DSN": sentry.dsn()})

	reporter.Report(context.Background(), coreprovider.ErrorEvent{Code: "boom"})
	sentry.waitFor(t, 1)

	sentry.mu.Lock()
	defer sentry.mu.Unlock()
	assert.Contains(t, sentry.auth[0], "sentry_key=publickey")
	assert.Contains(t, sentry.auth[0], "sentry_version=7")
	assert.Contains(t, sentry.auth[0], "sentry_client=gobit")
}

// TestTheEnvironmentFallsBackToAppEnv proves an operator does not say the same
// thing twice.
func TestTheEnvironmentFallsBackToAppEnv(t *testing.T) {
	t.Parallel()

	sentry := newCollector(t)
	reporter, _ := install(t, map[string]string{
		"SENTRY_DSN": sentry.dsn(),
		"APP_ENV":    "staging",
	})

	reporter.Report(context.Background(), coreprovider.ErrorEvent{Code: "boom"})

	assert.Equal(t, "staging", payloadOf(t, sentry.waitFor(t, 1)[0])["environment"])
}

// TestReportingIsAsynchronous proves the caller is not made to wait for a
// collector.
//
// The contract's promise is that Report does not block; a request that failed
// must not also become a slow request because a monitoring service is having a
// bad day.
func TestReportingIsAsynchronous(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() { close(release); server.Close() })

	reporter, _ := install(t, map[string]string{
		"SENTRY_DSN": strings.Replace(server.URL, "://", "://key@", 1) + "/42",
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		reporter.Report(context.Background(), coreprovider.ErrorEvent{Code: "boom"})
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Report waited for the collector")
	}
}

// TestARefusedReportIsNotRetried proves a collector outage does not turn into
// gobit spending its remaining capacity on it.
func TestARefusedReportIsNotRetried(t *testing.T) {
	t.Parallel()

	sentry := newCollector(t)
	sentry.mu.Lock()
	sentry.status = http.StatusTooManyRequests
	sentry.mu.Unlock()

	reporter, _ := install(t, map[string]string{"SENTRY_DSN": sentry.dsn()})
	reporter.Report(context.Background(), coreprovider.ErrorEvent{Code: "boom"})
	require.NoError(t, reporter.Close(context.Background()))

	assert.Len(t, sentry.envelopes(), 1, "one attempt, then the report is dropped")
}

// TestCloseFlushesWhatIsQueued proves the last reports before a shutdown are
// the ones that survive.
func TestCloseFlushesWhatIsQueued(t *testing.T) {
	t.Parallel()

	sentry := newCollector(t)
	reporter, _ := install(t, map[string]string{"SENTRY_DSN": sentry.dsn()})

	for range 20 {
		reporter.Report(context.Background(), coreprovider.ErrorEvent{Code: "boom"})
	}
	require.NoError(t, reporter.Close(context.Background()))

	assert.Len(t, sentry.envelopes(), 20,
		"the reports of the failures that ended the process are the ones worth having")
}

// TestReportingAfterCloseIsSafe proves shutdown ordering cannot panic the
// process.
//
// A request in flight can log after Close has run. Sending on a closed channel
// would panic there — inside a log handler, during shutdown, where nothing is
// left to recover it usefully.
func TestReportingAfterCloseIsSafe(t *testing.T) {
	t.Parallel()

	sentry := newCollector(t)
	reporter, _ := install(t, map[string]string{"SENTRY_DSN": sentry.dsn()})

	require.NoError(t, reporter.Close(context.Background()))
	assert.NotPanics(t, func() {
		reporter.Report(context.Background(), coreprovider.ErrorEvent{Code: "late"})
	})
	assert.NoError(t, reporter.Close(context.Background()), "closing twice is not an error")
}

// TestCloseGivesUpOnTheDeadline proves a collector that stopped answering does
// not hold the shutdown open.
func TestCloseGivesUpOnTheDeadline(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() { close(release); server.Close() })

	reporter, _ := install(t, map[string]string{
		"SENTRY_DSN": strings.Replace(server.URL, "://", "://key@", 1) + "/42",
	})
	reporter.Report(context.Background(), coreprovider.ErrorEvent{Code: "boom"})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := reporter.Close(ctx)

	require.Error(t, err)
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnavailable))
}

// TestACollectorOutageDoesNotAmplifyItself is the loop this design has to
// avoid, and it is only visible with the whole stack assembled.
//
// The reporter feeds on the LOG. When a send fails the sender has to say so,
// and saying so at ERROR would put the complaint straight back through the
// reporting handler: send fails, log, report, send fails, log again. The core's
// rate limit caps the spiral rather than letting it run forever, but capping it
// means the budget for genuinely unclassified failures is spent on the
// collector complaining about itself — during the exact incident somebody is
// trying to read.
//
// So the send failure is logged at WARN, below the reporting floor, and one
// logged failure must produce exactly ONE attempt to deliver it.
func TestACollectorOutageDoesNotAmplifyItself(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	sink := errorreport.NewSink()
	log := logger.New(logger.Options{
		Level:      slog.LevelDebug,
		Format:     "text",
		Output:     io.Discard,
		Middleware: errorreport.Middleware(sink, errorreport.Options{}),
	})

	c := container.New(log)
	host := coreplugin.NewHost(c, nil, nil, log,
		map[string]string{"SENTRY_DSN": strings.Replace(server.URL, "://", "://key@", 1) + "/42"})
	require.NoError(t, errorsentry.New().Setup(context.Background(), host))

	reporter, err := container.Resolve[coreprovider.ErrorReporter](c, coreplugin.ErrorReporterName)
	require.NoError(t, err)
	require.NoError(t, sink.Bind(reporter))

	log.Error("request ended with a server error",
		"error", coreerrors.Unavailable("db_unreachable", "the database is not reachable"))

	// The wait is long enough for a spiral to show itself: each turn of it is a
	// failed POST to a local server, which takes microseconds.
	time.Sleep(300 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, reporter.Close(ctx))

	assert.Equal(t, int64(1), attempts.Load(),
		"one logged failure must cost exactly one delivery attempt, however the delivery goes")
}

// TestAFullQueueDropsAndSaysHowMany proves an overflow is counted rather than
// hidden.
//
// A queue that silently absorbed the excess would make a burst that overflowed
// look exactly like a burst that did not happen, which is the one reading an
// operator must not be given during an incident.
func TestAFullQueueDropsAndSaysHowMany(t *testing.T) {
	t.Parallel()

	// The collector records every body AND holds the first request open, which
	// is what backs the queue up behind it. One server has to do both: a
	// blocked server that recorded nothing could not show the count, and a
	// recording server that never blocked would drain the queue as fast as it
	// filled.
	held := make(chan struct{})
	var once sync.Once
	sentry := &collector{status: http.StatusOK}
	sentry.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sentry.mu.Lock()
		sentry.contents = append(sentry.contents, string(body))
		sentry.mu.Unlock()

		once.Do(func() { <-held })
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(sentry.Close)

	reporter, _ := install(t, map[string]string{"SENTRY_DSN": sentry.dsn()})

	for range 1000 {
		reporter.Report(context.Background(), coreprovider.ErrorEvent{Code: "boom"})
	}
	close(held)
	require.NoError(t, reporter.Close(context.Background()))

	envelopes := sentry.envelopes()
	assert.Less(t, len(envelopes), 1000, "a bounded queue must actually be bounded")

	var dropped string
	for _, envelope := range envelopes {
		extra, _ := payloadOf(t, envelope)["extra"].(map[string]any)
		if value, ok := extra["dropped_by_full_queue"].(string); ok {
			dropped = value
		}
	}
	assert.NotEmpty(t, dropped, "an overflow must be reported, not silently absorbed")
}
