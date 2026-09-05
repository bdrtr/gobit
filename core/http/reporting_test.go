package http_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errorreport"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/core/logger"
)

// This file wires the transport to the error reporter and asserts what one
// failing request produces.
//
// It is here rather than in core/errorreport because the bug it exists
// for is only visible with BOTH halves assembled: the handler's diagnostic line
// and the access log's summary of the same request are two records, and neither
// package on its own can see that they are one incident.

// collectingReporter keeps what it was handed.
type collectingReporter struct{ events []provider.ErrorEvent }

func (c *collectingReporter) ID() string { return "collecting" }

func (c *collectingReporter) Report(_ context.Context, event provider.ErrorEvent) {
	c.events = append(c.events, event)
}

func (c *collectingReporter) Close(context.Context) error { return nil }

// reportingStack builds the transport chain with reporting turned on.
func reportingStack(t *testing.T, handler http.Handler) (http.Handler, *collectingReporter) {
	t.Helper()

	sink := errorreport.NewSink()
	reporter := &collectingReporter{}
	require.NoError(t, sink.Bind(reporter))

	log := logger.New(logger.Options{
		Level:      slog.LevelDebug,
		Format:     "text",
		Output:     io.Discard,
		Middleware: errorreport.Middleware(sink, errorreport.Options{}),
	})

	return corehttp.RequestID(corehttp.RequestLogger(log)(corehttp.Recoverer(log)(handler))), reporter
}

// TestAFailingRequestIsReportedOnce is the whole point of the marker.
//
// A 5xx is logged twice: once by the code that produced it, carrying the
// machine code, and once by the access log as a summary, carrying none. Both
// records are at ERROR. Reporting both doubles the volume and files every
// server error in the application under "unclassified" — the bucket that has to
// stay empty enough for a genuinely unclassified failure to show up in it.
func TestAFailingRequestIsReportedOnce(t *testing.T) {
	t.Parallel()

	stack, reporter := reportingStack(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corehttp.WriteError(r.Context(), w,
			coreerrors.Wrap(coreerrors.New("relation \"product\" does not exist"),
				coreerrors.KindInternal, "product_db_failed", "the products could not be listed"))
	}))

	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/products", http.NoBody))

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Len(t, reporter.events, 1, "one failure is one incident, however many lines it logs")

	event := reporter.events[0]
	assert.Equal(t, "product_db_failed", event.Code,
		"the surviving report must be the one that carries the fingerprint")
	assert.Equal(t, "the products could not be listed", event.Detail)
	assert.Equal(t, rec.Header().Get("X-Request-Id"), event.RequestID,
		"the report must carry the id the client was shown")
	assert.NotContains(t, event.Detail, "relation",
		"the driver's message stays in the process")
}

// TestAPanicIsReportedOnceAndKeepsItsStack proves the recoverer's line is the
// one that survives.
func TestAPanicIsReportedOnceAndKeepsItsStack(t *testing.T) {
	t.Parallel()

	stack, reporter := reportingStack(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("a nil map was written to")
	}))

	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/products", http.NoBody))

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Len(t, reporter.events, 1)

	event := reporter.events[0]
	assert.Equal(t, "the handler panicked", event.Message)
	assert.Contains(t, event.Attrs["panic"], "a nil map was written to")
	assert.NotEmpty(t, event.Stack, "a panic is the one failure with a stack worth having")
}

// TestASuccessfulRequestReportsNothing proves the marker did not turn the
// access log into a reporting channel for every request.
func TestASuccessfulRequestReportsNothing(t *testing.T) {
	t.Parallel()

	stack, reporter := reportingStack(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	stack.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/store/v1/products", http.NoBody))

	assert.Empty(t, reporter.events)
}

// TestTheMarkerTellsTheTruthInTheLogToo proves the flag is a statement, not
// just a switch.
//
// Nothing acts on it below ERROR, so a marker set unconditionally would change
// no behavior — and would still put "already_reported=true" on the access line
// of every successful request, which is false. A log that says something untrue
// costs an operator more than a log that says nothing.
func TestTheMarkerTellsTheTruthInTheLogToo(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		status int
		want   string
	}{
		{name: "a successful request is nobody's incident", status: http.StatusOK, want: "false"},
		{name: "a client error is the caller's", status: http.StatusNotFound, want: "false"},
		{name: "a server error was already reported by its handler",
			status: http.StatusInternalServerError, want: "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			log, buf := testLogger()
			stack := corehttp.RequestLogger(log)(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status) }))

			stack.ServeHTTP(httptest.NewRecorder(),
				httptest.NewRequest(http.MethodGet, "/store/v1/products", http.NoBody))

			records := logRecords(t, buf)
			require.Len(t, records, 1)
			assert.Equal(t, tc.want, fmt.Sprint(records[0][errorreport.KeyAlreadyReported]))
		})
	}
}

// TestAClientErrorIsNotAnIncident proves a 4xx is the caller's problem.
//
// It is logged at WARN, below the reporting floor, and a collector filling up
// with other people's typos is a collector nobody reads.
func TestAClientErrorIsNotAnIncident(t *testing.T) {
	t.Parallel()

	stack, reporter := reportingStack(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corehttp.WriteError(r.Context(), w,
			coreerrors.Invalid("product_handle_taken", "that handle is already used"))
	}))

	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/v1/products", http.NoBody))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Empty(t, reporter.events)
}
