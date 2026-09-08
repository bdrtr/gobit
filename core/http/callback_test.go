package http_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// testAck is the answer vocabulary of the test provider. It is deliberately
// NOT JSON: the
// measured provider reads the body, not the status, and this suite would not
// notice the core's envelope leaking onto the surface if the two looked alike.
var testAck = corehttp.CallbackAck{
	Accepted:    corehttp.CallbackResponse{Status: http.StatusOK, Body: "OK"},
	Duplicate:   corehttp.CallbackResponse{Status: http.StatusOK, Body: "DUP"},
	Rejected:    corehttp.CallbackResponse{Status: http.StatusForbidden, Body: "BAD_HASH"},
	Malformed:   corehttp.CallbackResponse{Status: http.StatusBadRequest, Body: "BAD_REQUEST"},
	Unavailable: corehttp.CallbackResponse{Status: http.StatusInternalServerError, Body: "RETRY"},
}

// callbackHarness is a mounted registry with a router in front of it.
type callbackHarness struct {
	registry *corehttp.CallbackRegistry
	router   chi.Router
	calls    *int
}

// newCallbackHarness builds a registry with one route and mounts it.
func newCallbackHarness(t *testing.T, opts corehttp.CallbackOptions,
	change ...func(*corehttp.CallbackRoute),
) *callbackHarness {
	t.Helper()

	calls := 0
	route := corehttp.CallbackRoute{
		Source:  "testpay",
		Path:    "/testpay/callback",
		Verify:  func(_ context.Context, r *http.Request, body []byte) error { return verifyTestBody(body) },
		Key:     testKey,
		Handler: func(w http.ResponseWriter, _ *http.Request) { calls++; _, _ = io.WriteString(w, "OK") },
		Ack:     testAck,
	}
	for _, apply := range change {
		apply(&route)
	}

	registry := corehttp.NewCallbackRegistry(opts)
	require.NoError(t, registry.Register(route))

	router := chi.NewRouter()
	router.Use(registry.Middleware())
	require.NoError(t, registry.Mount(router))

	return &callbackHarness{registry: registry, router: router, calls: &calls}
}

// post sends a callback body and returns what the surface answered.
func (h *callbackHarness) post(body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/testpay/callback", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	h.router.ServeHTTP(recorder, request)

	return recorder
}

// verifyTestBody stands in for a signature: a body must start with "signed:".
func verifyTestBody(body []byte) error {
	if !strings.HasPrefix(string(body), "signed:") {
		return errors.New("the signature does not match")
	}

	return nil
}

// testKey derives the identity from the first field and the content from both,
// the way a real route derives them from signature-covered fields only.
func testKey(_ *http.Request, body []byte) (identity, content []string, err error) {
	fields := strings.Split(strings.TrimPrefix(string(body), "signed:"), ",")
	if len(fields) == 0 || fields[0] == "" {
		return nil, nil, nil
	}

	return []string{fields[0]}, fields, nil
}

// TestAVerifiedCallbackReachesTheHandler is the baseline the rest measure against.
func TestAVerifiedCallbackReachesTheHandler(t *testing.T) {
	t.Parallel()

	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: newTestCallbackStore()})
	response := harness.post("signed:evt-1,paid")

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "OK", response.Body.String())
	require.Equal(t, 1, *harness.calls)
}

// TestAForgedCallbackNeverReachesTheHandler is the whole reason this ring exists.
//
// The measured endpoint's only credential is a signature inside the body, so a
// verification that runs after the handler — or not at all — protects nothing.
func TestAForgedCallbackNeverReachesTheHandler(t *testing.T) {
	t.Parallel()

	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: newTestCallbackStore()})
	response := harness.post("forged:evt-1,paid")

	require.Equal(t, http.StatusForbidden, response.Code)
	require.Equal(t, "BAD_HASH", response.Body.String())
	require.Zero(t, *harness.calls, "a request that failed verification reached the handler")
}

// TestAnOversizedBodyIsRefusedBeforeAnythingReadsIt bounds the one unbounded input.
func TestAnOversizedBodyIsRefusedBeforeAnythingReadsIt(t *testing.T) {
	t.Parallel()

	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: newTestCallbackStore()},
		func(rt *corehttp.CallbackRoute) { rt.MaxBodyBytes = 32 })
	response := harness.post("signed:" + strings.Repeat("x", 64))

	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Equal(t, "BAD_REQUEST", response.Body.String())
	require.Zero(t, *harness.calls)
}

// TestARetriedCallbackIsAnsweredFromTheRecord keeps a retry from paying twice.
func TestARetriedCallbackIsAnsweredFromTheRecord(t *testing.T) {
	t.Parallel()

	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: newTestCallbackStore()})

	first := harness.post("signed:evt-1,paid")
	second := harness.post("signed:evt-1,paid")

	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, http.StatusOK, second.Code)
	require.Equal(t, "OK", second.Body.String(), "the retry was not answered with the recorded body")
	require.Equal(t, "true", second.Header().Get("Idempotency-Replayed"))
	require.Equal(t, 1, *harness.calls, "the handler ran twice for one event")
}

// TestAContradictingRetryIsAcknowledgedAndNotApplied is the case a plain
// idempotency ring gets wrong.
//
// The same event id arriving with a different outcome is a real signal, not a
// client error — and it still has to be ACKNOWLEDGED, because a provider that
// reads the body would otherwise retry the contradiction forever.
func TestAContradictingRetryIsAcknowledgedAndNotApplied(t *testing.T) {
	t.Parallel()

	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: newTestCallbackStore()})

	harness.post("signed:evt-1,paid")
	second := harness.post("signed:evt-1,failed")

	require.Equal(t, http.StatusOK, second.Code, "a contradiction was refused; the provider will retry it forever")
	require.Equal(t, "DUP", second.Body.String())
	require.Equal(t, 1, *harness.calls, "the contradicting event was applied")
}

// TestTheContradictionCarriesACodeAnErrorCollectorCanGroupBy holds the half of
// ADR 0062 that a decision alone would not have made true.
//
// A callback has no ledger table (ADR 0062) and no audit row (ADR 0056), so the
// ring's log is the whole record of what it refused — and for an installation
// with a reporter the ERROR lines are the part of that record which leaves the
// process at all. A reporter fingerprints a record by the CODE of the error it
// carries, so a line carrying no error is filed under "unclassified": the
// contradiction would be invisible among every other unclassified failure and
// would spend that bucket's rate limit, which is three reports a minute for all
// of them together.
//
// The code is asserted as a LITERAL on purpose. The constant is unexported
// because it is never returned to a caller, and what actually consumes the
// string is an alert rule in somebody's collector — which is exactly a literal
// too. This is the test that stops it changing under them silently.
func TestTheContradictionCarriesACodeAnErrorCollectorCanGroupBy(t *testing.T) {
	t.Parallel()

	logger, captured := newCallbackLog()
	harness := newCallbackHarness(t, corehttp.CallbackOptions{
		Logger: logger, Store: newTestCallbackStore(),
	})

	harness.post("signed:evt-1,paid")
	captured.reset()
	harness.post("signed:evt-1,failed")

	said := captured.said()
	require.NotEmpty(t, said, "the contradiction left no line at all")

	carried := slices.IndexFunc(said, func(line loggedLine) bool {
		_, has := line.values["error"]

		return has
	})
	require.GreaterOrEqual(t, carried, 0,
		"the contradiction wrote %d line(s) and none of them carries an error value.\n"+
			"An error reporter reads the code off the error; a line that carries only a "+
			"SENTENCE is reported as unclassified, and the one outcome whose own message "+
			"says a human has to look is then the hardest one in the collector to find.",
		len(said))

	failure, isError := said[carried].values["error"].(error)
	require.True(t, isError, "the error attribute is not an error value")
	require.Equal(t, "callback_contradiction", coreerrors.CodeOf(failure),
		"the contradiction's code changed.\n"+
			"It is the string an operator's alert rule matches on, and nothing else names "+
			"it: the constant behind it is unexported because it is never returned to a "+
			"caller. Changing it silently retires every alert built on it.")
}

// TestTwoSourcesDoNotShareAReplayNamespace is why the key carries the source.
func TestTwoSourcesDoNotShareAReplayNamespace(t *testing.T) {
	t.Parallel()

	store := newTestCallbackStore()
	registry := corehttp.NewCallbackRegistry(corehttp.CallbackOptions{Store: store})

	calls := map[string]int{}
	for _, source := range []string{"alpha", "beta"} {
		require.NoError(t, registry.Register(corehttp.CallbackRoute{
			Source: source,
			Path:   "/" + source + "/callback",
			Verify: func(_ context.Context, _ *http.Request, body []byte) error { return verifyTestBody(body) },
			Key:    testKey,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				calls[strings.Split(r.URL.Path, "/")[1]]++
				_, _ = io.WriteString(w, "OK")
			},
			Ack: testAck,
		}))
	}

	router := chi.NewRouter()
	router.Use(registry.Middleware())
	require.NoError(t, registry.Mount(router))

	for _, source := range []string{"alpha", "beta"} {
		request := httptest.NewRequest(http.MethodPost, "/"+source+"/callback",
			strings.NewReader("signed:evt-1,paid"))
		router.ServeHTTP(httptest.NewRecorder(), request)
	}

	require.Equal(t, map[string]int{"alpha": 1, "beta": 1},
		calls, "one provider's event silenced another's; the replay key is not namespaced by source")
}

// TestAnUnreachableReplayWindowRefusesRatherThanRisksApplyingTwice states the
// direction this ring fails in.
func TestAnUnreachableReplayWindowRefusesRatherThanRisksApplyingTwice(t *testing.T) {
	t.Parallel()

	store := newTestCallbackStore()
	store.beginErr = errors.New("redis is unreachable")
	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: store})

	response := harness.post("signed:evt-1,paid")

	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Equal(t, "RETRY", response.Body.String())
	require.Zero(t, *harness.calls, "the event was applied with no replay window")
}

// TestAnEventInFlightIsToldToRetry keeps two concurrent deliveries from both
// being applied.
func TestAnEventInFlightIsToldToRetry(t *testing.T) {
	t.Parallel()

	store := newTestCallbackStore()
	store.beginErr = corehttp.ErrIdempotencyKeyInFlight
	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: store})

	response := harness.post("signed:evt-1,paid")

	require.Equal(t, "RETRY", response.Body.String())
	require.Zero(t, *harness.calls)
}

// TestAFailedHandlerIsNotRecorded keeps a transient fault from becoming permanent.
func TestAFailedHandlerIsNotRecorded(t *testing.T) {
	t.Parallel()

	store := newTestCallbackStore()
	attempts := 0
	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: store},
		func(rt *corehttp.CallbackRoute) {
			rt.Handler = func(w http.ResponseWriter, _ *http.Request) {
				attempts++
				if attempts == 1 {
					w.WriteHeader(http.StatusInternalServerError)

					return
				}
				_, _ = io.WriteString(w, "OK")
			}
		})

	first := harness.post("signed:evt-1,paid")
	second := harness.post("signed:evt-1,paid")

	require.Equal(t, http.StatusInternalServerError, first.Code)
	require.Equal(t, http.StatusOK, second.Code)
	require.Equal(t, "OK", second.Body.String(),
		"the retry was answered from a recorded FAILURE; the event can never be applied")
	require.Equal(t, 2, attempts)
}

// TestAThrottledCallbackIsToldToRetry gives the surface the quota it has none of.
func TestAThrottledCallbackIsToldToRetry(t *testing.T) {
	t.Parallel()

	harness := newCallbackHarness(t, corehttp.CallbackOptions{
		Store:   newTestCallbackStore(),
		Limiter: refusingLimiter{},
	})

	response := harness.post("signed:evt-1,paid")

	require.Equal(t, "RETRY", response.Body.String())
	require.Zero(t, *harness.calls, "a throttled callback still did the work")
}

// TestAnUnkeyableCallbackStillRuns keeps a payload this route cannot key from
// being refused on this repository's authority.
func TestAnUnkeyableCallbackStillRuns(t *testing.T) {
	t.Parallel()

	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: newTestCallbackStore()})
	response := harness.post("signed:")

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, 1, *harness.calls)
}

// TestNoCallbackOutcomeIsSilent holds the record ADR 0056 leaves on this ring.
//
// # What the log is evidence ABOUT, and why the population is drawn here
//
// A record whose population is the callbacks that PASSED the guards is evidence
// about the guards and about nothing else. The forged signature, the throttled
// flood, the payload this route cannot key: every one of them is a request a
// guard stopped, and every one of them is why somebody opens the log. So the
// population is every outcome the ring can produce, refusals first — which is
// also the shape any durable ledger would have to keep.
//
// # Why the cases are written out and not derived
//
// Deriving them from the ring's own branches would derive the population from
// the property under audit: a branch that says nothing would drop OUT of the
// list rather than fail it, and the gate would go quiet exactly when a new
// outcome went silent. The list is a hand-written census, and the census is
// wrong in the safe direction — it can miss a new outcome, and it cannot be
// emptied by one.
//
// Each case asserts a line NAMING the callback. An unattributed line is not a
// record: nobody reading a log full of other traffic can find it.
func TestNoCallbackOutcomeIsSilent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		outcome string
		// wantStatus is the status the callback-naming line must CARRY, or ZERO
		// for an outcome whose line does not carry one.
		//
		// Asserting the line's EXISTENCE alone leaves the attribute unheld, and
		// that was measured rather than supposed: deleting `"status",
		// recorder.status` from the guard left this whole test green. For the
		// four outcomes where the handler ran, the status is the half of the
		// record an operator reads first — the difference between "the provider
		// was answered" and "the provider was answered 500 and will retry
		// forever".
		//
		// The nine REFUSALS carry zero, and that is a boundary rather than an
		// oversight: their lines predate ADR 0056, each already names its own
		// reason ("a callback was throttled", "a callback failed verification"),
		// and the status follows from the reason. Widening them is a change to
		// nine established messages and belongs to whoever wants it, not to the
		// record that added the four.
		wantStatus int
		// drive performs the act. Anything it does BEFORE calling reset is
		// setup, so a case about a retry is judged on the retry alone.
		drive func(t *testing.T, logger *slog.Logger, reset func())
	}{
		{
			outcome:    "throttled",
			wantStatus: 0, // a refusal; see the field comment
			drive: func(t *testing.T, logger *slog.Logger, _ func()) {
				newCallbackHarness(t, corehttp.CallbackOptions{
					Logger: logger, Store: newTestCallbackStore(), Limiter: refusingLimiter{},
				}).post("signed:evt-1,paid")
			},
		},
		{
			outcome:    "oversized body",
			wantStatus: 0, // a refusal; see the field comment
			drive: func(t *testing.T, logger *slog.Logger, _ func()) {
				newCallbackHarness(t,
					corehttp.CallbackOptions{Logger: logger, Store: newTestCallbackStore()},
					func(rt *corehttp.CallbackRoute) { rt.MaxBodyBytes = 32 },
				).post("signed:" + strings.Repeat("x", 64))
			},
		},
		{
			outcome:    "forged signature",
			wantStatus: 0, // a refusal; see the field comment
			drive: func(t *testing.T, logger *slog.Logger, _ func()) {
				newCallbackHarness(t, corehttp.CallbackOptions{
					Logger: logger, Store: newTestCallbackStore(),
				}).post("forged:evt-1,paid")
			},
		},
		{
			outcome:    "key derivation failed",
			wantStatus: 0, // a refusal; see the field comment
			drive: func(t *testing.T, logger *slog.Logger, _ func()) {
				newCallbackHarness(t,
					corehttp.CallbackOptions{Logger: logger, Store: newTestCallbackStore()},
					func(rt *corehttp.CallbackRoute) {
						rt.Key = func(*http.Request, []byte) (identity, content []string, err error) {
							return nil, nil, errors.New("the verified payload could not be keyed")
						}
					},
				).post("signed:evt-1,paid")
			},
		},
		{
			outcome:    "unkeyable payload",
			wantStatus: 0, // a refusal; see the field comment
			drive: func(t *testing.T, logger *slog.Logger, _ func()) {
				newCallbackHarness(t, corehttp.CallbackOptions{
					Logger: logger, Store: newTestCallbackStore(),
				}).post("signed:")
			},
		},
		{
			// The installation with no replay window. It answers the provider
			// exactly as a guarded one does, so the log is the only place it is
			// visible at all.
			outcome:    "no replay window",
			wantStatus: 200,
			drive: func(t *testing.T, logger *slog.Logger, _ func()) {
				newCallbackHarness(t, corehttp.CallbackOptions{Logger: logger}).
					post("signed:evt-1,paid")
			},
		},
		{
			outcome:    "handled",
			wantStatus: 200,
			drive: func(t *testing.T, logger *slog.Logger, _ func()) {
				newCallbackHarness(t, corehttp.CallbackOptions{
					Logger: logger, Store: newTestCallbackStore(),
				}).post("signed:evt-1,paid")
			},
		},
		{
			outcome:    "handler failed",
			wantStatus: 500,
			drive: func(t *testing.T, logger *slog.Logger, _ func()) {
				newCallbackHarness(t,
					corehttp.CallbackOptions{Logger: logger, Store: newTestCallbackStore()},
					func(rt *corehttp.CallbackRoute) {
						rt.Handler = func(w http.ResponseWriter, _ *http.Request) {
							w.WriteHeader(http.StatusInternalServerError)
						}
					},
				).post("signed:evt-1,paid")
			},
		},
		{
			outcome:    "replayed",
			wantStatus: 0, // a refusal; see the field comment
			drive: func(t *testing.T, logger *slog.Logger, reset func()) {
				harness := newCallbackHarness(t, corehttp.CallbackOptions{
					Logger: logger, Store: newTestCallbackStore(),
				})
				harness.post("signed:evt-1,paid")
				reset()
				harness.post("signed:evt-1,paid")
			},
		},
		{
			outcome:    "contradiction",
			wantStatus: 0, // a refusal; see the field comment
			drive: func(t *testing.T, logger *slog.Logger, reset func()) {
				harness := newCallbackHarness(t, corehttp.CallbackOptions{
					Logger: logger, Store: newTestCallbackStore(),
				})
				harness.post("signed:evt-1,paid")
				reset()
				harness.post("signed:evt-1,failed")
			},
		},
		{
			outcome:    "event in flight",
			wantStatus: 0, // a refusal; see the field comment
			drive: func(t *testing.T, logger *slog.Logger, _ func()) {
				store := newTestCallbackStore()
				store.beginErr = corehttp.ErrIdempotencyKeyInFlight
				newCallbackHarness(t, corehttp.CallbackOptions{Logger: logger, Store: store}).
					post("signed:evt-1,paid")
			},
		},
		{
			outcome:    "replay window unreachable",
			wantStatus: 0, // a refusal; see the field comment
			drive: func(t *testing.T, logger *slog.Logger, _ func()) {
				store := newTestCallbackStore()
				store.beginErr = errors.New("redis is unreachable")
				newCallbackHarness(t, corehttp.CallbackOptions{Logger: logger, Store: store}).
					post("signed:evt-1,paid")
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.outcome, func(t *testing.T) {
			t.Parallel()

			logger, captured := newCallbackLog()
			testCase.drive(t, logger, captured.reset)

			said := captured.said()
			require.NotEmpty(t, said,
				"the %q outcome left NO line at all.\n"+
					"A callback has no audit row (ADR 0056), so this log is the whole "+
					"record of it: an outcome that says nothing happened without a trace.",
				testCase.outcome)

			named := slices.IndexFunc(said, func(line loggedLine) bool {
				return line.attrs["callback"] == "testpay"
			})
			require.GreaterOrEqual(t, named, 0,
				"the %q outcome wrote %d line(s) and none of them names the callback.\n"+
					"An unattributed line is not a record: in a log carrying every other "+
					"request there is no way to find it, and no way to tell which provider "+
					"it was about.", testCase.outcome, len(said))

			if testCase.wantStatus == 0 {
				return
			}
			assert.Equal(t, strconv.Itoa(testCase.wantStatus), said[named].attrs["status"],
				"the %q outcome named the callback but did not say what the provider was "+
					"ANSWERED.\n"+
					"A callback has no audit row (ADR 0056), so this line is the whole "+
					"record, and a record of a request without its outcome cannot answer "+
					"the question it exists for: whether the provider will come back.",
				testCase.outcome)
		})
	}
}

// loggedLine is one record the ring wrote, flattened to what a test can ask.
type loggedLine struct {
	message string
	attrs   map[string]string
	// values are the same attributes UNRENDERED. The census reads attrs
	// because what it audits is a rendered field; a reader asking what an error
	// collector would FINGERPRINT the line by needs the error itself, and its
	// rendering carries no code.
	values map[string]any
}

// callbackLog captures what the ring said.
//
// It is a slog.Handler rather than a text buffer because the property under
// audit is an ATTRIBUTE — that the line names the callback it is about — and a
// formatted line would make the test match on the ring's wording instead.
type callbackLog struct {
	mu    sync.Mutex
	lines []loggedLine
}

// newCallbackLog builds a logger and the capture behind it.
func newCallbackLog() (*slog.Logger, *callbackLog) {
	captured := &callbackLog{}

	return slog.New(&callbackLogHandler{log: captured}), captured
}

// reset drops what was captured so far, so a case can be judged on its act
// rather than on its setup.
func (l *callbackLog) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.lines = nil
}

// said returns what has been captured.
func (l *callbackLog) said() []loggedLine {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.lines)
}

// callbackLogHandler is one view of a [callbackLog], carrying the attributes
// bound to it by slog.Logger's With.
//
// The ring binds the source and the path exactly once, at the top of the guard,
// and every line inherits them from there — so a handler that dropped what With
// gave it would report every line as unattributed and the gate would fail on a
// fault of its own making.
type callbackLogHandler struct {
	log   *callbackLog
	attrs []slog.Attr
}

// Enabled admits every level: an outcome logged at DEBUG is still an outcome.
func (h *callbackLogHandler) Enabled(context.Context, slog.Level) bool { return true }

// Handle flattens one record into the capture.
func (h *callbackLogHandler) Handle(_ context.Context, record slog.Record) error {
	line := loggedLine{
		message: record.Message,
		attrs:   map[string]string{},
		values:  map[string]any{},
	}
	for _, attr := range h.attrs {
		line.attrs[attr.Key] = attr.Value.String()
		line.values[attr.Key] = attr.Value.Any()
	}
	record.Attrs(func(attr slog.Attr) bool {
		line.attrs[attr.Key] = attr.Value.String()
		line.values[attr.Key] = attr.Value.Any()

		return true
	})

	h.log.mu.Lock()
	defer h.log.mu.Unlock()
	h.log.lines = append(h.log.lines, line)

	return nil
}

// WithAttrs returns a view carrying the given attributes as well.
func (h *callbackLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	bound := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	bound = append(bound, h.attrs...)
	bound = append(bound, attrs...)

	return &callbackLogHandler{log: h.log, attrs: bound}
}

// WithGroup is not used by this ring and nests nothing.
func (h *callbackLogHandler) WithGroup(string) slog.Handler { return h }

// TestNothingIsGuardedBeforeMount states the direction the ring fails in while
// it is still being built.
func TestNothingIsGuardedBeforeMount(t *testing.T) {
	t.Parallel()

	registry := corehttp.NewCallbackRegistry(corehttp.CallbackOptions{Store: newTestCallbackStore()})
	require.NoError(t, registry.Register(corehttp.CallbackRoute{
		Source: "testpay", Path: "/testpay/callback",
		Verify:  func(context.Context, *http.Request, []byte) error { return nil },
		Key:     testKey,
		Handler: func(http.ResponseWriter, *http.Request) {},
		Ack:     testAck,
	}))

	router := chi.NewRouter()
	router.Use(registry.Middleware())

	request := httptest.NewRequest(http.MethodPost, "/testpay/callback", strings.NewReader("signed:x"))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNotFound, recorder.Code,
		"an unmounted callback answered; the route is not bound, so nothing could have run")
}

// TestRegisterRefusesWhatItCannotGuard checks every startup refusal.
func TestRegisterRefusesWhatItCannotGuard(t *testing.T) {
	t.Parallel()

	sound := func() corehttp.CallbackRoute {
		return corehttp.CallbackRoute{
			Source: "testpay", Path: "/testpay/callback",
			Verify:  func(context.Context, *http.Request, []byte) error { return nil },
			Key:     testKey,
			Handler: func(http.ResponseWriter, *http.Request) {},
			Ack:     testAck,
		}
	}

	cases := map[string]func(*corehttp.CallbackRoute){
		"no source":       func(rt *corehttp.CallbackRoute) { rt.Source = " " },
		"relative path":   func(rt *corehttp.CallbackRoute) { rt.Path = "testpay/callback" },
		"pattern path":    func(rt *corehttp.CallbackRoute) { rt.Path = "/testpay/{id}" },
		"wildcard path":   func(rt *corehttp.CallbackRoute) { rt.Path = "/testpay/*" },
		"no verification": func(rt *corehttp.CallbackRoute) { rt.Verify = nil },
		"no key":          func(rt *corehttp.CallbackRoute) { rt.Key = nil },
		"no handler":      func(rt *corehttp.CallbackRoute) { rt.Handler = nil },
		"no accepted answer": func(rt *corehttp.CallbackRoute) {
			rt.Ack.Accepted = corehttp.CallbackResponse{}
		},
		"no retry answer": func(rt *corehttp.CallbackRoute) {
			rt.Ack.Unavailable = corehttp.CallbackResponse{}
		},
	}

	for name, break_ := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			route := sound()
			break_(&route)
			require.Error(t, corehttp.NewCallbackRegistry(corehttp.CallbackOptions{}).Register(route),
				"a callback with %s was accepted; it would be reachable and unguarded", name)
		})
	}
}

// TestOnePathCannotHaveTwoProviders refuses the collision at startup.
func TestOnePathCannotHaveTwoProviders(t *testing.T) {
	t.Parallel()

	registry := corehttp.NewCallbackRegistry(corehttp.CallbackOptions{})
	route := corehttp.CallbackRoute{
		Source: "alpha", Path: "/shared/callback",
		Verify:  func(context.Context, *http.Request, []byte) error { return nil },
		Key:     testKey,
		Handler: func(http.ResponseWriter, *http.Request) {},
		Ack:     testAck,
	}

	require.NoError(t, registry.Register(route))
	route.Source = "beta"
	require.Error(t, registry.Register(route))
}

// TestRegisteringAfterMountIsLoud keeps a late route from being silently unbound.
func TestRegisteringAfterMountIsLoud(t *testing.T) {
	t.Parallel()

	registry := corehttp.NewCallbackRegistry(corehttp.CallbackOptions{})
	require.NoError(t, registry.Mount(chi.NewRouter()))

	require.Error(t, registry.Register(corehttp.CallbackRoute{
		Source: "late", Path: "/late/callback",
		Verify:  func(context.Context, *http.Request, []byte) error { return nil },
		Key:     testKey,
		Handler: func(http.ResponseWriter, *http.Request) {},
		Ack:     testAck,
	}), "a route registered after mounting was accepted; it would never be bound")
}

// refusingLimiter always says no.
type refusingLimiter struct{}

// Allow refuses every key.
func (refusingLimiter) Allow(context.Context, string) (corehttp.Decision, error) {
	return corehttp.Decision{Allowed: false, Limit: 1, RetryAfter: time.Second}, nil
}

// testCallbackStore is an in-test idempotency store with injectable faults.
type testCallbackStore struct {
	mu       sync.Mutex
	records  map[string]corehttp.IdempotentResponse
	inFlight map[string]bool
	beginErr error
}

// newTestCallbackStore builds an empty store.
func newTestCallbackStore() *testCallbackStore {
	return &testCallbackStore{
		records:  map[string]corehttp.IdempotentResponse{},
		inFlight: map[string]bool{},
	}
}

// Begin reserves the key or returns what was recorded for it.
func (s *testCallbackStore) Begin(_ context.Context, key, _ string) (
	*corehttp.IdempotentResponse, bool, error,
) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.beginErr != nil {
		return nil, false, s.beginErr
	}
	if record, done := s.records[key]; done {
		return &record, true, nil
	}
	if s.inFlight[key] {
		return nil, false, corehttp.ErrIdempotencyKeyInFlight
	}
	s.inFlight[key] = true

	return nil, false, nil
}

// Complete records the answer.
func (s *testCallbackStore) Complete(_ context.Context, key string,
	response corehttp.IdempotentResponse,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.inFlight, key)
	s.records[key] = response

	return nil
}

// Abort releases the reservation.
func (s *testCallbackStore) Abort(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.inFlight, key)

	return nil
}
