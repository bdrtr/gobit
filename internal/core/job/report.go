package job

import (
	"context"
	"sync"
)

// reporterKey is the context key the run's [Reporter] is stored under.
//
// An unexported empty struct, so nothing outside this package can reach the
// value or collide with the key. The only doors in are [Report] and
// [Reporter.Detail].
type reporterKey struct{}

// Reporter collects the one line a run leaves for the operator.
//
// # Why it is exported when only the runner installs one
//
// Because [Report] is a NO-OP without one in the context, and a job's own unit
// test calls the job's run function directly rather than through a runner. A
// test with no way to install a reporter would have to assert against silence —
// which is exactly how a reporting channel rots into a capability nobody uses.
// internal/jobs's three job tests each take one from [WithReporter] and read
// [Reporter.Detail] back.
type Reporter struct {
	// mu guards detail for CORRECTNESS, not for speed. A job may report from a
	// goroutine it started, and the runner reads the line back on the goroutine
	// that called the job — two different goroutines, so the read needs to be
	// defined rather than merely likely.
	mu sync.Mutex
	// detail is the last line reported.
	detail string
}

// WithReporter attaches a fresh [Reporter] to ctx and returns both.
//
// A FRESH one per call, never a shared one: the reporter's whole content is one
// run's line, and a reporter reused across occurrences would let last night's
// number stand as tonight's when a run reported nothing.
func WithReporter(ctx context.Context) (context.Context, *Reporter) {
	r := &Reporter{}

	return context.WithValue(ctx, reporterKey{}, r), r
}

// Detail returns the last line reported, or the empty string if nothing was.
func (r *Reporter) Detail() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.detail
}

// set stores a reported line, replacing whatever was there.
func (r *Reporter) set(detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.detail = detail
}

// Report leaves the operator one line from INSIDE a run.
//
// # The hole it fills
//
// [Outcome.Detail] is the DETAIL column of `gobit jobs`, and until this existed
// the runner could fill it from exactly one place: an ERROR carrying a
// JobDetail method. A run that SUCCEEDED therefore had, by construction,
// nowhere to put a number — and two separate pieces of work walked into that
// wall from opposite sides. The outbox relay makes its dead-letter pile visible
// by FAILING the run, because failing is the only channel there was, and the
// payment plugin's stuck-payment watch can only log. Neither of them wanted a
// failure; both wanted a sentence.
//
// # Why a context value, and not a second return value
//
// The obvious fix is to widen [Func] to func(context.Context) (string, error).
// It was measured and refused, and the measurement is the argument:
// [github.com/bdrtr/gobit/core/plugin.Job] repeats Run as
// func(context.Context) error because a published package may not import an
// internal one, and internal/app/jobs_test.go's
// TestEveryJobDefinitionFieldReachesAPluginJob requires every exported field of
// [Definition] to have a twin on that published struct whose type CONVERTS. Go
// converts neither direction between func types whose results differ — checked,
// both ways report false — so widening Func either breaks that gate or forces a
// breaking change on a contract published one day earlier. That is a large bill
// for handing three in-repo jobs a string, and the version of it that gets paid
// by a downstream author is worse: their plugin stops compiling.
//
// The third candidate was an interface a SUCCESSFUL result may implement, and
// it cannot be built at all. Success is a nil error and nil implements nothing,
// so the only way to carry a value out of a successful run through the existing
// signature is to return a non-nil error that is not a failure — after which
// every "if err != nil" in the runner, the failure column of job_run and the
// "FAILED:" prefix in the listing each have to learn which errors are not
// failures. Turning the sentinel into the norm is a worse trade than the one
// below.
//
// # What this one costs, said plainly
//
// It HIDES the channel. Nothing in a job's signature says it may report, so a
// reader has to already know this function exists — the standard and correct
// complaint against carrying anything in a context. Three things pay it down:
// [Func]'s own documentation names it, all three jobs in internal/jobs call it
// so the pattern is readable in the repository rather than promised in a
// godoc, and a call made outside a run is a silent no-op rather than a panic —
// so the cost of not knowing is a missing line, never a dead process.
//
// # The rules
//
// The LAST call wins. A job that builds its line as it goes may call this more
// than once, and the record keeps whatever it said last; that is what lets a
// run cut off by MaxRun still say how far it got. It is NOT logging: the line
// becomes one cell of a tabwriter table an operator reads during an incident,
// so it must be a single line and it must carry no personal data.
//
// An error carrying a JobDetail method still OVERRIDES anything reported here.
// A failing run's line is about the failure, and that precedence is what keeps
// every run that failed before this existed reporting exactly what it reported
// then.
func Report(ctx context.Context, detail string) {
	reporter, ok := ctx.Value(reporterKey{}).(*Reporter)
	if !ok {
		// No reporter means nobody is recording an outcome: a job function
		// called straight from a test, or the hand-run path (see
		// [Runner.RunNow]), which writes no row at all. Silence is the right
		// answer to both — the alternative is a panic in the one place a job
		// is being exercised deliberately.
		return
	}

	reporter.set(detail)
}
