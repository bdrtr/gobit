package paymentpaytr

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/jobreport"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
)

// This file is the plugin's scheduled half, and it exists because the report it
// carries was only ever made ONCE.
//
// [paytrModule.reportStuck] runs inside Register, so an installation is told
// about payments PayTR never reported on at the instant it boots and never
// again. The class it names is money that may have been taken with no order
// behind it (see the package documentation and the migration's own comment),
// and that class does not arrive at startup — it accumulates in a process that
// has been up for a week. Before this job, a long-lived process watched it
// accumulate in complete silence, and the only other way to see it was for
// somebody to think of calling GET /admin/v1/paytr/pending.
//
// # It reads and reports. It does not act.
//
// Nothing here writes, retries, cancels, refunds or completes anything. ADR
// 0017 refuses running side effects on a schedule nobody watched, and the
// repair for a stuck PayTR payment is a person looking at the list. What
// changes is that the person learns there is something to look at.
//
// This is deliberately the same shape as internal/jobs/sagawatch, which was
// authorized by exactly this argument for a different class of half-finished
// work.

// JobName is the scheduled watch's name.
//
// It is the advisory lock's input and the primary key of the job's history, so
// it is a CONTRACT: changing it starts a new job with no history. It is
// prefixed with the plugin's own name because jobs share ONE namespace with the
// core's and with every other plugin's, and a clash is refused at startup.
const JobName = ModuleName + "-pending-watch"

// jobEvery is how often the watch runs.
//
// Hourly, and it is the same hour as [pendingGrace] on purpose: a payment is
// not reported until it has been pending for that long, so a shorter interval
// would ask the same question again about the same rows and find the same
// answer. A longer one would let a genuinely new stuck payment sit unmentioned
// for most of a shift.
const jobEvery = time.Hour

// jobMaxRun bounds one pass.
//
// It is a tripwire rather than a budget: the pass makes one bounded query
// against one table of this plugin's own, and a pass that has not finished in a
// minute means something about the shape of the data or of the connection
// changed. The bound also has to stay below [jobEvery] or the scheduler refuses
// the definition at boot.
const jobMaxRun = time.Minute

// pendingWatchLimit caps one pass, and the cap being hit is REPORTED as hit.
//
// The startup report had the same cap and did not say when it reached it, so an
// incident that left four hundred payments pending was described as one
// hundred. A number that silently means "at least this many" is worse than no
// number: it is the one an operator would use to decide the problem is small.
const pendingWatchLimit = 100

// codeWatchFailed reports that the pending listing could not be read.
const codeWatchFailed = "paytr_pending_watch_failed"

// codeWatchNotReady reports a pass that ran before the module had a store.
const codeWatchNotReady = "paytr_pending_watch_not_ready"

// pendingWatch builds the job the plugin declares to the host.
//
// The module is captured rather than the store: the store does not exist yet
// when Setup runs — [paytrModule.Register] builds it once the pool is in the
// container — and a job that captured a nil store would keep it forever.
func pendingWatch(m *paytrModule) coreplugin.Job {
	return coreplugin.Job{
		Name:   JobName,
		Every:  jobEvery,
		MaxRun: jobMaxRun,
		Run:    m.watchPending,
	}
}

// watchPending logs how many payments PayTR never reported on.
//
// # Why finding stuck payments is not a FAILED run
//
// The outbox relay fails its run when its dead-letter pile is not empty, and
// that is right there: the pile has an operator command that empties it, so the
// alarm has an off switch. This one has none. A payment PayTR never called
// about stays pending forever — no command clears it and no later event moves
// it — so failing the run would leave `gobit jobs` showing FAILED for that job
// permanently, from the first orphan onwards. An alarm that is always on is an
// alarm that gets filtered out, and it would take the OTHER jobs' failures with
// it the day an operator learns to skim past a red line.
//
// So the report is a log line at WARN, which is what
// [paytrModule.reportStuck] already used, and the run succeeds. This is
// internal/jobs/sagawatch's choice, for the same reason.
//
// # The count also goes where an operator looks FIRST
//
// A log line at WARN is read by somebody who already went to the logs. The
// question this watch answers — "is anything stuck?" — is asked from
// `gobit jobs`, and until ADR 0069 published
// [github.com/bdrtr/gobit/core/jobreport] a plugin's job could not put anything
// in that listing's DETAIL column without FAILING, which is precisely the
// alarm this job refuses to raise. So the number the pass computes is reported
// on every pass, the quiet ones included: "0 pending" every hour is the
// sentence that makes the hour it becomes 3 legible.
func (m *paytrModule) watchPending(ctx context.Context) error {
	if m.store == nil {
		// Register did not run, or did not get as far as the pool. Returning an
		// error rather than nil is the point: a silent success here would put
		// an "ok" in `gobit jobs` for a watch that looked at nothing.
		//
		// It is checked on the CONCRETE field, before the conversion below: a
		// nil *store handed to an interface is a non-nil interface holding a
		// nil pointer, and this check would stop firing without a word.
		return coreerrors.Unavailable(codeWatchNotReady,
			"the %s module has no store; the pending payment watch cannot read anything",
			ModuleName)
	}

	return runPendingWatch(ctx, m.store, m.log)
}

// pendingLister is the narrow surface one pass needs.
//
// It is declared HERE rather than the pass taking the whole store, so the work
// depends on the one method it uses and a TEST CAN SUPPLY IT without a
// database. internal/jobs/sagawatch's reader exists for the same reason, and
// there is a second one now: the line this pass reports is only assertable if
// the pass can be run at all, and a channel whose content nothing asserts is
// the capability-with-no-consumer this repository names most often.
type pendingLister interface {
	pending(ctx context.Context, olderThan time.Duration, limit int) ([]payment, error)
}

// runPendingWatch is one pass over the pending payments.
func runPendingWatch(ctx context.Context, rows pendingLister, log *slog.Logger) error {
	stuck, err := rows.pending(ctx, pendingGrace, pendingWatchLimit)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeWatchFailed,
			"the pending PayTR payments could not be listed")
	}

	jobreport.Report(ctx, summarize(len(stuck)))

	if len(stuck) == 0 {
		// DEBUG rather than INFO. A healthy installation runs this every hour
		// forever, and a line that never changes is a line nobody reads — which
		// is how the one that differs gets missed.
		log.DebugContext(ctx, "no PayTR payment is waiting on a callback",
			"older_than", pendingGrace.String())

		return nil
	}

	log.WarnContext(ctx, "PayTR payments are still pending; PayTR never reported on them "+
		"and any money taken has no order behind it",
		"pending", len(stuck),
		// Hit means "this many OR MORE". It is a separate key rather than a
		// suffix on the count so that a log search can filter on it.
		"truncated", len(stuck) == pendingWatchLimit,
		"older_than", pendingGrace.String(),
		"list", PendingPath)

	return nil
}

// summarize renders one pass as the single line `gobit jobs` prints.
//
// The payment ids stay in the LOG and in the listing behind [PendingPath]; they
// do not come here. This is one cell of a table an operator scans, and a pass
// that found a hundred stuck payments would push every other job's row off the
// terminal — the ids are useless without the next step anyway, which is reading
// that endpoint.
//
// A filled cap is said OUT LOUD, for the reason [pendingWatchLimit] exists: a
// number that silently means "at least this many" is the one an operator would
// use to decide the problem is small.
func summarize(pending int) string {
	if pending == 0 {
		return "0 pending"
	}

	line := fmt.Sprintf("%d payment(s) pending for over %s", pending, pendingGrace)
	if pending == pendingWatchLimit {
		line += fmt.Sprintf("; the limit of %d was filled, so there may be more",
			pendingWatchLimit)
	}

	return line
}
