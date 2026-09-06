package paymentpaytr

import (
	"context"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
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
func (m *paytrModule) watchPending(ctx context.Context) error {
	if m.store == nil {
		// Register did not run, or did not get as far as the pool. Returning an
		// error rather than nil is the point: a silent success here would put
		// an "ok" in `gobit jobs` for a watch that looked at nothing.
		return coreerrors.Unavailable(codeWatchNotReady,
			"the %s module has no store; the pending payment watch cannot read anything",
			ModuleName)
	}

	stuck, err := m.store.pending(ctx, pendingGrace, pendingWatchLimit)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeWatchFailed,
			"the pending PayTR payments could not be listed")
	}

	if len(stuck) == 0 {
		// DEBUG rather than INFO. A healthy installation runs this every hour
		// forever, and a line that never changes is a line nobody reads — which
		// is how the one that differs gets missed.
		m.log.DebugContext(ctx, "no PayTR payment is waiting on a callback",
			"older_than", pendingGrace.String())

		return nil
	}

	m.log.WarnContext(ctx, "PayTR payments are still pending; PayTR never reported on them "+
		"and any money taken has no order behind it",
		"pending", len(stuck),
		// Hit means "this many OR MORE". It is a separate key rather than a
		// suffix on the count so that a log search can filter on it.
		"truncated", len(stuck) == pendingWatchLimit,
		"older_than", pendingGrace.String(),
		"list", PendingPath)

	return nil
}
