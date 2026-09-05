package errorreport_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errorreport"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/core/logger"
)

// fakeReporter records what the core decided to send.
type fakeReporter struct {
	mu       sync.Mutex
	events   []provider.ErrorEvent
	closed   bool
	panicNow bool
}

func (f *fakeReporter) ID() string { return "fake" }

func (f *fakeReporter) Report(_ context.Context, event provider.ErrorEvent) {
	if f.panicNow {
		panic("the reporter is broken")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
}

func (f *fakeReporter) Close(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true

	return nil
}

func (f *fakeReporter) all() []provider.ErrorEvent {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]provider.ErrorEvent{}, f.events...)
}

func (f *fakeReporter) one(t *testing.T) provider.ErrorEvent {
	t.Helper()

	events := f.all()
	require.Len(t, events, 1, "exactly one report was expected")

	return events[0]
}

// harness is a logger wired to a sink, with the log output kept.
type harness struct {
	log      *slog.Logger
	out      *bytes.Buffer
	sink     *errorreport.Sink
	reporter *fakeReporter
}

// newHarness builds the logger the way cmd/server does.
//
// The limiter is given a burst of 100 so the rate limit does not interfere with
// the tests that are not about it; the ones that are build their own.
func newHarness(t *testing.T, opts errorreport.Options) *harness {
	t.Helper()

	if opts.Limiter == nil {
		opts.Limiter = errorreport.NewLimiter(100, time.Minute, nil)
	}

	h := &harness{out: &bytes.Buffer{}, sink: errorreport.NewSink(), reporter: &fakeReporter{}}
	h.log = logger.New(logger.Options{
		Level:      slog.LevelDebug,
		Format:     "text",
		Output:     h.out,
		Middleware: errorreport.Middleware(h.sink, opts),
	})

	return h
}

// bind installs the fake reporter.
func (h *harness) bind(t *testing.T) *harness {
	t.Helper()
	require.NoError(t, h.sink.Bind(h.reporter))

	return h
}

// TestAnUnboundSinkChangesNothing proves the usual installation — the one with
// no collector — pays nothing but a nil check.
func TestAnUnboundSinkChangesNothing(t *testing.T) {
	t.Parallel()

	h := newHarness(t, errorreport.Options{})

	h.log.Error("request ended with a server error", "code", "product_not_found")

	assert.Contains(t, h.out.String(), "product_not_found",
		"the log line is written whether or not anything reports it")
	assert.Nil(t, h.sink.Reporter())
}

// TestTheLogSurvivesABrokenReporter is the ordering claim.
//
// A collector in another datacenter must never be able to cost the operator the
// log line, which is the durable record. The reporter panicking is the harshest
// version of "broken" and the log still has to be there.
func TestTheLogSurvivesABrokenReporter(t *testing.T) {
	t.Parallel()

	h := newHarness(t, errorreport.Options{}).bind(t)
	h.reporter.panicNow = true

	h.log.Error("request ended with a server error", "code", "product_not_found")

	assert.Contains(t, h.out.String(), "product_not_found")
}

// TestTheLogIsWrittenBeforeTheReport is the ordering claim, and a slow
// collector is what makes it matter.
//
// A reporter that hangs — a TCP connect to a collector that stopped answering,
// a full queue — must not hold the log line hostage. The log is the durable
// record and it is written first; a panic is caught by the sink, but nothing
// can catch a call that simply does not return.
func TestTheLogIsWrittenBeforeTheReport(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	blocked := make(chan struct{})
	h := newHarness(t, errorreport.Options{})
	require.NoError(t, h.sink.Bind(&blockingReporter{entered: blocked, release: release}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.log.Error("request ended with a server error", "code", "product_not_found")
	}()

	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("the reporter was never called")
	}

	assert.Contains(t, h.out.String(), "product_not_found",
		"the log line must already be written while the reporter is still busy")

	close(release)
	<-done
}

// blockingReporter stops inside Report until it is released.
type blockingReporter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingReporter) ID() string { return "blocking" }

func (b *blockingReporter) Report(context.Context, provider.ErrorEvent) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
}

func (b *blockingReporter) Close(context.Context) error { return nil }

// TestAPanickingReporterIsSwitchedOff proves one bad plugin cannot take the
// process with it.
//
// The loop it prevents is real: reporting happens inside a log handler, inside a
// request, inside the panic recoverer. A panic here is recovered up there, the
// recovery LOGS, and the log comes straight back into this handler.
func TestAPanickingReporterIsSwitchedOff(t *testing.T) {
	t.Parallel()

	h := newHarness(t, errorreport.Options{}).bind(t)
	h.reporter.panicNow = true
	h.log.Error("the handler panicked", "code", "internal")

	h.reporter.panicNow = false
	h.log.Error("request ended with a server error", "code", "product_not_found")

	assert.Empty(t, h.reporter.all(),
		"a reporter that panicked once is not asked again for the life of the process")
}

// TestOnlyFailuresAreReported proves the level is a floor, not a suggestion.
func TestOnlyFailuresAreReported(t *testing.T) {
	t.Parallel()

	h := newHarness(t, errorreport.Options{}).bind(t)

	h.log.Debug("cache miss")
	h.log.Info("request completed")
	h.log.Warn("the panel could not read the stock levels")
	h.log.Error("request ended with a server error")

	events := h.reporter.all()
	require.Len(t, events, 1)
	assert.Equal(t, "request ended with a server error", events[0].Message)
}

// TestReportingRidesOnTheLogLevel proves a level the operator turned off stays
// off.
//
// Widening it here would send a collector records that exist in no log — an
// observability integration inventing observations.
func TestReportingRidesOnTheLogLevel(t *testing.T) {
	t.Parallel()

	h := &harness{out: &bytes.Buffer{}, sink: errorreport.NewSink(), reporter: &fakeReporter{}}
	h.log = logger.New(logger.Options{
		Level:      slog.LevelError + 1,
		Format:     "text",
		Output:     h.out,
		Middleware: errorreport.Middleware(h.sink, errorreport.Options{}),
	})
	require.NoError(t, h.sink.Bind(h.reporter))

	h.log.Error("request ended with a server error")

	assert.Empty(t, h.out.String())
	assert.Empty(t, h.reporter.all(), "nothing was logged, so there is nothing to report")
}

// TestTheCodeIsTheFingerprint proves the grouping key comes off the typed
// error.
func TestTheCodeIsTheFingerprint(t *testing.T) {
	t.Parallel()

	h := newHarness(t, errorreport.Options{}).bind(t)

	h.log.Error("request ended with a server error",
		"error", coreerrors.Unavailable("pricing_unavailable", "the price service is not reachable"))

	event := h.reporter.one(t)
	assert.Equal(t, "pricing_unavailable", event.Code)
	assert.Equal(t, "unavailable", event.Kind)
	assert.Equal(t, "the price service is not reachable", event.Detail,
		"the typed error's own Message is documented as carrying nothing sensitive")
}

// TestAnUnclassifiedErrorGroupsUnderAName proves a missing code becomes a class
// rather than an empty string.
func TestAnUnclassifiedErrorGroupsUnderAName(t *testing.T) {
	t.Parallel()

	h := newHarness(t, errorreport.Options{}).bind(t)

	h.log.Error("request ended with a server error", "error", coreerrors.New("boom"))

	event := h.reporter.one(t)
	assert.Equal(t, "unclassified", event.Code)
	assert.Equal(t, "internal", event.Kind, "an unclassified error is a server error")
	assert.Empty(t, event.Detail)
}

// TestTheWrappedChainNeverLeavesTheProcess is the confidentiality claim.
//
// The chain under a typed error is written by drivers and libraries that
// promise nothing about their contents. This is where a connection string, a
// bound query parameter or a customer's address would be, and the reporter is
// never handed it — not as a field it could choose to ignore, but at all.
func TestTheWrappedChainNeverLeavesTheProcess(t *testing.T) {
	t.Parallel()

	h := newHarness(t, errorreport.Options{}).bind(t)
	underneath := coreerrors.New(
		"dial tcp 10.0.0.5:5432: password authentication failed for user \"gobit\"")

	h.log.Error("request ended with a server error",
		"error", coreerrors.Wrap(underneath, coreerrors.KindUnavailable,
			"db_unreachable", "the catalog is temporarily unavailable"))

	event := h.reporter.one(t)
	rendered := renderAll(event)
	assert.NotContains(t, rendered, "10.0.0.5")
	assert.NotContains(t, rendered, "password")
	assert.Equal(t, "the catalog is temporarily unavailable", event.Detail)
	assert.Equal(t, "db_unreachable", event.Code, "the fingerprint still works")
}

// renderAll flattens everything an event carries into one string.
func renderAll(event provider.ErrorEvent) string {
	parts := []string{event.Message, event.Code, event.Kind, event.Detail, event.Stack,
		event.RequestID, event.TraceID, event.SpanID}
	for key, value := range event.Attrs {
		parts = append(parts, key, value)
	}
	parts = append(parts, event.Redacted...)

	return strings.Join(parts, "\x00")
}

// TestAnAttributeOutsideTheAllowListIsRedactedButNamed proves the default is
// refusal and that the refusal is visible.
func TestAnAttributeOutsideTheAllowListIsRedactedButNamed(t *testing.T) {
	t.Parallel()

	h := newHarness(t, errorreport.Options{}).bind(t)

	h.log.Error("request ended with a server error",
		"path", "/admin/v1/products",
		"customer_email", "someone@example.com",
		"cart_id", "cart_01H")

	event := h.reporter.one(t)
	assert.Equal(t, "/admin/v1/products", event.Attrs["path"])
	assert.NotContains(t, renderAll(event), "someone@example.com")
	assert.NotContains(t, renderAll(event), "cart_01H")
	assert.Equal(t, []string{"cart_id", "customer_email"}, event.Redacted,
		"the keys travel so a missing field is not mistaken for a field never set")
}

// TestABusinessIdentifierIsNotInTheDefaultAllowList states the policy choice
// out loud.
//
// user_id and its kind are logged all over this repository and every one of
// them points at a particular person's records. A report is a COPY leaving the
// building; an installation that wants them in its collector adds them itself.
func TestABusinessIdentifierIsNotInTheDefaultAllowList(t *testing.T) {
	t.Parallel()

	h := newHarness(t, errorreport.Options{}).bind(t)

	h.log.Error("request ended with a server error", "user_id", "user_01H", "order_id", "order_01H")

	event := h.reporter.one(t)
	assert.Empty(t, event.Attrs)
	assert.Equal(t, []string{"order_id", "user_id"}, event.Redacted)
}

// TestAnInstallationCanWidenTheAllowList proves the policy is a decision an
// operator can make, not a rule they must live with.
func TestAnInstallationCanWidenTheAllowList(t *testing.T) {
	t.Parallel()

	policy := errorreport.DefaultPolicy()
	policy.Allow = append(policy.Allow, "user_id")
	h := newHarness(t, errorreport.Options{Policy: policy}).bind(t)

	h.log.Error("request ended with a server error", "user_id", "user_01H")

	assert.Equal(t, "user_01H", h.reporter.one(t).Attrs["user_id"])
}

// TestTheCorrelationHandlesGetTheirOwnFields proves they are lifted out of the
// attribute map rather than copied into both places.
func TestTheCorrelationHandlesGetTheirOwnFields(t *testing.T) {
	t.Parallel()

	h := newHarness(t, errorreport.Options{}).bind(t)

	h.log.Error("request ended with a server error",
		"request_id", "req_01H", "trace_id", "abcd", "span_id", "ef01", "stack", "goroutine 1")

	event := h.reporter.one(t)
	assert.Equal(t, "req_01H", event.RequestID)
	assert.Equal(t, "abcd", event.TraceID)
	assert.Equal(t, "ef01", event.SpanID)
	assert.Equal(t, "goroutine 1", event.Stack)
	assert.Empty(t, event.Attrs, "a lifted field must not also appear as an ordinary attribute")
}

// TestAttributesCarriedByTheLoggerTravelToo proves With and WithGroup are not
// lost.
//
// A request logger is built with logger.With("request_id", …) and the attribute
// never appears in the Error call itself. A handler that read only the record's
// own attributes would report every failure without its correlation handle —
// the one field that makes a report actionable.
func TestAttributesCarriedByTheLoggerTravelToo(t *testing.T) {
	t.Parallel()

	h := newHarness(t, errorreport.Options{}).bind(t)

	h.log.With("request_id", "req_01H").
		WithGroup("http").With("method", "POST").
		Error("request ended with a server error", "status", 500)

	event := h.reporter.one(t)
	assert.Equal(t, "req_01H", event.RequestID)
	assert.Equal(t, []string{"http.method", "http.status"}, event.Redacted,
		"a grouped attribute is judged under its qualified name, not its bare one")
}

// TestARepeatedFailureIsLimitedPerCode proves an outage does not become a
// report storm.
func TestARepeatedFailureIsLimitedPerCode(t *testing.T) {
	t.Parallel()

	h := newHarness(t, errorreport.Options{
		Limiter: errorreport.NewLimiter(2, time.Minute, nil),
	}).bind(t)

	for range 50 {
		h.log.Error("request ended with a server error",
			"error", coreerrors.Unavailable("db_unreachable", "the database is not reachable"))
	}

	assert.Len(t, h.reporter.all(), 2)
}

// TestTwoFailuresDoNotShareOneBudget proves the grouping is per code.
//
// An overall limit would fill with whichever endpoint is busiest and hide every
// other failure behind it — the rare one being exactly the one worth seeing.
func TestTwoFailuresDoNotShareOneBudget(t *testing.T) {
	t.Parallel()

	h := newHarness(t, errorreport.Options{
		Limiter: errorreport.NewLimiter(1, time.Minute, nil),
	}).bind(t)

	for range 20 {
		h.log.Error("noisy", "error", coreerrors.Unavailable("db_unreachable", "down"))
	}
	h.log.Error("rare", "error", coreerrors.Internal("checkout_broken", "a rare bug"))

	events := h.reporter.all()
	require.Len(t, events, 2)
	assert.Equal(t, "db_unreachable", events[0].Code)
	assert.Equal(t, "checkout_broken", events[1].Code,
		"the rare failure must not be crowded out by the common one")
}

// TestTheSuppressedCountRidesTheNextReport proves the magnitude survives even
// though the individual occurrences do not.
func TestTheSuppressedCountRidesTheNextReport(t *testing.T) {
	t.Parallel()

	clock := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	h := newHarness(t, errorreport.Options{
		Limiter: errorreport.NewLimiter(1, time.Minute, now),
	}).bind(t)

	fail := func() {
		h.log.Error("request ended with a server error",
			"error", coreerrors.Unavailable("db_unreachable", "down"))
	}

	fail()
	for range 9 {
		fail()
	}

	clock = clock.Add(2 * time.Minute)
	fail()

	events := h.reporter.all()
	require.Len(t, events, 2)
	assert.Zero(t, events[0].Suppressed)
	assert.Equal(t, 9, events[1].Suppressed,
		"an outage that produced ten failures must not look like one that produced two")
}

// TestASecondReporterIsRefused proves the sink is bound once.
func TestASecondReporterIsRefused(t *testing.T) {
	t.Parallel()

	sink := errorreport.NewSink()
	require.NoError(t, sink.Bind(&fakeReporter{}))

	err := sink.Bind(&fakeReporter{})

	require.Error(t, err)
	assert.True(t, coreerrors.IsConflict(err))
	assert.Equal(t, "fake", sink.Reporter().ID(), "the first one keeps the job")
}

// TestCloseReachesTheReporter proves the shutdown flush is wired.
//
// It is the one call a reporter may block in: the reports of the failures that
// happened just before the process stopped are the ones most worth having, and
// an unflushed queue is exactly what loses them.
func TestCloseReachesTheReporter(t *testing.T) {
	t.Parallel()

	sink := errorreport.NewSink()
	reporter := &fakeReporter{}
	require.NoError(t, sink.Bind(reporter))

	require.NoError(t, sink.Close(context.Background()))
	assert.True(t, reporter.closed)
	assert.NoError(t, errorreport.NewSink().Close(context.Background()),
		"closing an unbound sink is not an error")
}
