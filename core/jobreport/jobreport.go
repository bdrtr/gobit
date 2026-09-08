// Package jobreport carries the one line a scheduled run leaves for the
// operator.
//
// The work a job does is func(ctx context.Context) error, and that signature
// has room for exactly one thing: a failure. A run that SUCCEEDED therefore had
// nowhere to put a number, so the DETAIL column of `gobit jobs` could only ever
// be filled by an error — which is how a relay came to FAIL its run in order to
// mention its dead letters. [Report], called with the context the run was
// handed, is the second channel, and the line it leaves reaches that column
// whether the run succeeds or fails.
//
// # Why the channel is published and the scheduler is not
//
// The runner, its store contract, its advisory-lock class and its occurrence
// election live in internal/core/job and they stay there. ADR 0026's amendment
// measured that a plugin needs the four VALUES of a job definition rather than
// the machine that consumes them, and publishing the machine would freeze the
// store, the key algorithm and the election into a promise kept until 1.0.0.
//
// The channel is the one part of the scheduler a plugin has to NAME. A plugin's
// job runs under the same runner as the core's, and until this package existed
// it could not say a word: the call lived in an internal package, and
// [github.com/bdrtr/gobit/core/plugin.Job] may not import one. So ADR 0069
// moved the CHANNEL out and left the scheduler where it was — the same shape
// ADR 0014 used for the error reporter, where the contract sits in the core and
// the implementation sits in a plugin.
//
// # It is ONE mechanism, not a second one
//
// The three jobs the core registers report through this package too. A
// published channel for plugins beside an internal one for the core would be
// two ways to say one thing, and the drift between them would land in the one
// column an operator reads during an incident.
//
// # What it costs, said plainly
//
// It HIDES the channel. Nothing in a job's signature says it may report, so a
// reader has to already know this package exists — the standard and correct
// complaint against carrying anything in a context. Three things pay it down:
// [github.com/bdrtr/gobit/core/plugin.Job]'s Run field names it, five jobs in
// this repository call it so the pattern is readable rather than promised, and
// a call made outside a run is a silent no-op rather than a panic — so the cost
// of not knowing is a missing line, never a dead process.
package jobreport

import (
	"context"
	"sync"
)

// reporterKey is the context key a run's reporter is stored under.
//
// An unexported empty struct, so nothing outside this package can reach the
// value or collide with the key. The only doors in are [Report] and
// [Detail].
type reporterKey struct{}

// reporter collects the one line a run leaves for the operator.
//
// # Why the handle is UNEXPORTED and the read-back is a function
//
// [Report] is a NO-OP without a reporter in the context, and a job's own unit
// test calls the run function directly rather than through a runner. A test
// with no way to install one would have to assert against silence — which is
// exactly how a reporting channel rots into a capability nobody uses. So a
// caller outside this package must be able to install a reporter and read the
// line back.
//
// That needs two FUNCTIONS, not a type. [WithReporter] returns a context and
// [Detail] reads it back, which is the shape this repository already publishes
// three times over in core/http — RequestIDFromContext, LoggerFromContext,
// PrincipalFromContext. Publishing the struct as well would promise its
// identity, its method set and its zero value until 1.0.0 in exchange for
// nothing a caller cannot do with the pair.
type reporter struct {
	// mu guards detail for CORRECTNESS, not for speed. A job may report from a
	// goroutine it started, and the runner reads the line back on the goroutine
	// that called the job — two different goroutines, so the read needs to be
	// defined rather than merely likely.
	mu sync.Mutex
	// detail is the last line reported.
	detail string
}

// WithReporter attaches a fresh reporter to ctx.
//
// A FRESH one per call, never a shared one: its whole content is ONE run's
// line, and a reporter reused across occurrences would let last night's number
// stand as tonight's when a run reported nothing.
func WithReporter(ctx context.Context) context.Context {
	return context.WithValue(ctx, reporterKey{}, &reporter{})
}

// Detail returns the last line reported into ctx, or the empty string — both
// when nothing was reported and when no reporter was installed.
//
// The two are deliberately the same answer. A caller reading a detail wants the
// line or nothing; whether the run was silent or unattached is the runner's
// question, and it knows because it is the one that attached.
func Detail(ctx context.Context) string {
	r, ok := ctx.Value(reporterKey{}).(*reporter)
	if !ok {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.detail
}

// set stores a reported line, replacing whatever was there.
func (r *reporter) set(detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.detail = detail
}

// Report leaves the operator one line from INSIDE a run.
//
// # Why a context value, and not a second return value
//
// The obvious fix is to widen the job's work to func(context.Context) (string,
// error). It was measured and refused, and the measurement is the argument:
// [github.com/bdrtr/gobit/core/plugin.Job] repeats Run as
// func(context.Context) error because a published package may not import an
// internal one, and internal/app's TestEveryJobDefinitionFieldReachesAPluginJob
// requires every exported field of the scheduler's own definition to have a
// twin on that published struct whose type CONVERTS. Go converts neither
// direction between func types whose results differ — checked, both ways report
// false — so widening the signature either breaks that gate or forces a
// BREAKING change on a published struct. That bill is paid by the downstream
// author, whose plugin stops compiling, and it buys three in-repo jobs and two
// in-repo plugins a string.
//
// The third candidate was an interface a SUCCESSFUL result may implement, and
// it cannot be built at all. Success is a nil error and nil implements nothing,
// so the only way to carry a value out of a successful run through the existing
// signature is to return a non-nil error that is not a failure — after which
// every "if err != nil" in the runner, the failure column of the job history
// and the "FAILED:" prefix in the listing each have to learn which errors are
// not failures. Turning the sentinel into the norm is a worse trade than the
// hidden channel this package's documentation admits to.
//
// # The rules
//
// The LAST call wins. A job that builds its line as it goes may call this more
// than once, and the record keeps whatever it said last; that is what lets a
// run cut off by its deadline still say how far it got. It is NOT logging: the
// line becomes one cell of a tabwriter table an operator reads during an
// incident, so it must be a single line and it must carry no personal data.
//
// An error carrying a JobDetail method still OVERRIDES anything reported here.
// A failing run's line is about the failure, and that precedence is what keeps
// every run that failed before this channel existed reporting exactly what it
// reported then.
func Report(ctx context.Context, detail string) {
	held, ok := ctx.Value(reporterKey{}).(*reporter)
	if !ok {
		// No reporter means nobody is recording an outcome: a job function
		// called straight from a test, or a hand-run that writes no row at all.
		// Silence is the right answer to both — the alternative is a panic in
		// the one place a job is being exercised deliberately.
		return
	}

	held.set(detail)
}
