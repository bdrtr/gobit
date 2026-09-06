// Package job runs work on a schedule, once across every instance.
//
// # Why this exists at all, and what it deliberately does not do
//
// gobit had no scheduled work of any kind, and for most of what people reach
// for a scheduler to do it still does not need one: an expiring campaign is
// already refused at READ time, so a job that flipped its status would change
// nothing observable. A capability with no consumer is this repository's named
// second error class (ADR 0009), so every job the binary runs was
// pre-authorized rather than invented: it shipped with ONE, and the two that
// followed each closed a hole somebody had already written down.
//
// ADR 0016 built the operator's read surface for half-finished sagas and left
// one half of it explicitly unclaimed: "It is a snapshot, not an alert. Nobody
// is told a cart is stuck." ADR 0017 then refused a scheduled SWEEPER, and the
// distinction is the whole reason this package can exist: what 0017 refuses is
// running compensations — side effects — unwatched. Watching and reporting is
// what 0016 left open.
//
// So: no job here ever undoes anything. That is narrower than "no job writes",
// and the narrower line is the true one — internal/jobs/outboxrelay publishes
// and marks rows, because sending a message a committed transaction already
// promised to send is not a compensation. The two watchers
// (internal/jobs/sagawatch, internal/jobs/paymentrecon) only read.
//
// # Election and liveness are two different questions
//
// A row answers "has this occurrence already run?" — that is frequency and
// history. A lock answers "is a process running this job right now?" — that is
// concurrency and liveness. They are composed because each fails at the other's
// job:
//
// A lock alone excludes concurrency but not frequency. Three replicas ticking
// on independent phases each find the lock free at a different moment, so a
// daily job runs three times a night — and gets worse as you scale out.
//
// A lease alone inverts hung and dead. A wedged-but-alive process keeps
// renewing and is never taken over, while a process that died releases nothing
// until a timer nobody can tune correctly expires. gobit's existing lease is
// weaker still: there is no lease column, no owner and no heartbeat anywhere in
// the schema — it is a caller-side predicate over updated_at.
//
// A session-scoped advisory lock has no dial at all. The backend exits,
// PostgreSQL reaps the lock, the next tick proceeds. That is why it is the
// liveness half.
package job

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// LockClass is the CLASS number of this package's advisory locks, written into
// the UPPER 32 bits of every key it takes.
//
// The key space of pg_advisory_lock is a SINGLE one across the whole database,
// so an unqualified hash of a job name would collide with somebody else's lock
// without either side knowing. The classes taken so far:
//
//	0            golang-migrate's schema lock — crc32(name)*salt, a uint32,
//	             which occupies the whole of class 0
//	1            the order module's per-customer spending lock
//	0x6C696E6B   the link module's declaration lock ("link_def")
//
// Class 0 is the one that matters. golang-migrate waits on its lock with
// pg_advisory_lock on context.Background(), so the wait is unbounded AND
// uncancellable; a job key that landed in that range could block a boot
// migration on a wait nobody can interrupt.
const LockClass int64 = 2

// Error codes.
const (
	// CodeDuplicate reports two jobs registered under one name.
	CodeDuplicate = "job_duplicate_name"
	// CodeInvalidDefinition reports a job that cannot run as declared.
	CodeInvalidDefinition = "job_definition_invalid"
	// CodeUnknown reports a name no job answers to.
	CodeUnknown = "job_unknown"
	// CodeRunFailed reports that the job itself returned an error.
	CodeRunFailed = "job_run_failed"
	// CodePanicked reports that the job panicked.
	CodePanicked = "job_panicked"
)

// Func is the work a job does.
//
// It MUST be safe to run twice: election makes a second concurrent run
// unlikely, not impossible — a process can be partitioned from the database
// after taking the lock, and the row that elects an occurrence is written
// before the work rather than after. Anything that cannot tolerate that does
// not belong in a job.
type Func func(ctx context.Context) error

// Definition is one scheduled job.
type Definition struct {
	// Name identifies the job. It is the advisory lock's input, the row's
	// primary key and what `gobit jobs` prints, so it is a CONTRACT: changing
	// it starts a new job with no history and lets the old one run again.
	Name string

	// Every is the interval between occurrences.
	//
	// It is an interval and not a calendar expression, and that is a decision.
	// A cron expression carries a time zone, and a time zone carries daylight
	// saving — which means an hour that happens twice and an hour that does not
	// happen at all. "Every 24 hours" has neither. A job that genuinely must
	// run at 02:00 local time belongs behind the operator's own cron calling
	// `gobit job run`.
	Every time.Duration

	// MaxRun bounds one run. The context handed to Run carries it as a
	// deadline.
	//
	// A job with no bound is a job that can outlive the shutdown window and be
	// killed halfway with the lock still held — which is survivable, because the
	// lock dies with the backend, but the operator sees nothing explaining why.
	MaxRun time.Duration

	// Run is the work.
	Run Func
}

// validate reports whether the definition can run.
func (d Definition) validate() error {
	switch {
	case d.Name == "":
		return coreerrors.Invalid(CodeInvalidDefinition, "a job needs a name")
	case d.Every <= 0:
		return coreerrors.Invalid(CodeInvalidDefinition,
			"the %q job needs a positive interval; %s was given", d.Name, d.Every)
	case d.MaxRun <= 0:
		return coreerrors.Invalid(CodeInvalidDefinition,
			"the %q job needs a positive MaxRun; a run with no bound cannot be reported on", d.Name)
	case d.MaxRun > d.Every:
		// Not merely untidy: a run that outlasts its own interval means the
		// next occurrence is due before this one finished, so the job is
		// permanently behind and the listing never shows it caught up.
		return coreerrors.Invalid(CodeInvalidDefinition,
			"the %q job's MaxRun (%s) exceeds its interval (%s); it could never catch up",
			d.Name, d.MaxRun, d.Every)
	case d.Run == nil:
		return coreerrors.Invalid(CodeInvalidDefinition, "the %q job has no Run function", d.Name)
	}

	return nil
}

// LockKey is the advisory lock key for a job name.
//
// FNV-1a, widened losslessly into the lower 32 bits, with [LockClass] above it.
// The digest does not need to be cryptographic — it only needs to produce the
// same number for the same name in every process and every version.
func LockKey(name string) int64 {
	h := fnv.New32a()
	// hash.Hash.Write never returns an error (a documented contract).
	_, _ = h.Write([]byte(name))

	return LockClass<<32 | int64(h.Sum32())
}

// Registry holds the jobs an application declared.
//
// Jobs are registered at the COMPOSITION ROOT, the same place modules are, and
// that is still true of the ones a plugin brings: ADR 0019 deferred a
// plugin-facing registration with a condition rather than a refusal — "it
// arrives with the first plugin that brings a job" — and the plugin now
// DECLARES while the composition root ADMITS. Every definition in here went
// through internal/app's registerJobs, and every one of them was validated by
// [Registry.Add] before it could reach a runner.
//
// A plugin cannot name [Definition]: this package is internal, so a plugin
// written outside the module could not declare one. It fills in
// [github.com/bdrtr/gobit/core/plugin.Job] instead, and the composition root is
// the single place that translates.
type Registry struct {
	byName map[string]Definition
	order  []string
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{byName: map[string]Definition{}}
}

// Add registers a job.
//
// A duplicate name is an ERROR rather than a replacement. Two jobs under one
// name would share an advisory lock and a history row, so one of them would
// silently never run — and which one would depend on registration order.
func (r *Registry) Add(d Definition) error {
	if err := d.validate(); err != nil {
		return err
	}
	if _, exists := r.byName[d.Name]; exists {
		return coreerrors.Invalid(CodeDuplicate,
			"a job named %q is already registered", d.Name)
	}

	r.byName[d.Name] = d
	r.order = append(r.order, d.Name)

	return nil
}

// Definitions returns the jobs in registration order.
func (r *Registry) Definitions() []Definition {
	out := make([]Definition, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.byName[name])
	}

	return out
}

// Get returns one job by name.
func (r *Registry) Get(name string) (Definition, error) {
	d, ok := r.byName[name]
	if !ok {
		return Definition{}, coreerrors.NotFound(CodeUnknown, "there is no job named %q", name)
	}

	return d, nil
}

// Len reports how many jobs are registered.
func (r *Registry) Len() int { return len(r.order) }

// Store is the durable half: it elects an occurrence and records the outcome.
//
// The interface lives here rather than in the implementation so the runner can
// be tested without a database, and so the one implementation
// (internal/core/job/jobpg) can be replaced without touching this package.
type Store interface {
	// Claim elects THIS process to run the job's occurrence at due.
	//
	// It returns false when another process already holds the occurrence or
	// already ran it. Both must be reported the same way: the caller's only
	// correct response to either is to do nothing.
	//
	// The claim covers frequency; the caller separately holds the lock that
	// covers liveness, and Claim must be called INSIDE it.
	Claim(ctx context.Context, name string, due time.Time) (claimed bool, err error)

	// Finish records how the run ended. It is called for a failure too: a run
	// that failed is a run that HAPPENED, and hiding it would make the listing
	// claim the job has not run since the last success.
	Finish(ctx context.Context, name string, due time.Time, outcome Outcome) error

	// Last reports what is known about each job, for the operator's listing.
	Last(ctx context.Context, names []string) (map[string]Run, error)

	// WithLock runs fn while holding the job's advisory lock, and reports
	// whether the lock was taken at all.
	//
	// The lock is SESSION scoped and taken with the try form. Session, because
	// a job is many statements and often no transaction — pg_advisory_xact_lock
	// outside a transaction releases immediately and protects nothing. Try,
	// because the blocking form waits without a bound.
	WithLock(ctx context.Context, key int64, fn func(context.Context) error) (locked bool, err error)
}

// Outcome is how a run ended.
type Outcome struct {
	// Err is nil on success.
	Err error
	// Duration is how long the run took.
	Duration time.Duration
	// Detail is a short line the job may leave for the operator, such as a
	// count. It is NOT free-form logging: it appears in `gobit jobs`, so it has
	// to be one line and it must carry no personal data.
	Detail string
}

// Run is one recorded occurrence.
type Run struct {
	// Name is the job.
	Name string
	// Due is the occurrence instant this run belongs to.
	Due time.Time
	// StartedAt and EndedAt bracket the run; EndedAt is zero while it is still
	// running or if the process died before recording an end.
	StartedAt time.Time
	EndedAt   time.Time
	// Failure is empty on success.
	Failure string
	// Detail is [Outcome.Detail].
	Detail string
}

// Succeeded reports whether the run finished without error.
func (r Run) Succeeded() bool { return !r.EndedAt.IsZero() && r.Failure == "" }

// Unfinished reports a run that started and never recorded an end.
//
// It is the shape a process that died mid-run leaves behind, and it is worth
// naming because it looks identical to a run in progress. The two are
// distinguished by the LOCK, not by this record: if nobody holds the job's
// lock, nobody is running it.
func (r Run) Unfinished() bool { return !r.StartedAt.IsZero() && r.EndedAt.IsZero() }

// Occurrence returns the most recent occurrence instant at or before now.
//
// Occurrences are anchored to the epoch rather than to process start, and that
// is what makes the election work across instances: two processes that started
// minutes apart compute the SAME due instant, so exactly one of them wins the
// row. Anchoring to start-up would give every replica its own timeline and the
// row would never collide.
func Occurrence(every time.Duration, now time.Time) time.Time {
	if every <= 0 {
		return now.UTC().Truncate(time.Second)
	}

	return now.UTC().Truncate(every)
}

// errPanicked wraps a recovered panic value.
var errPanicked = errors.New("the job panicked")

// safely runs fn, converting a panic into an error.
//
// A panicking job must not take the process down with it: the same rule the
// event bus applies to a subscriber. What it MUST NOT do is disappear — the
// panic becomes a failed run in the listing and a reported error, which is the
// difference between "the job is broken" and "the job silently stopped".
func safely(ctx context.Context, fn Func) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = coreerrors.Wrap(fmt.Errorf("%w: %v", errPanicked, v),
				coreerrors.KindInternal, CodePanicked, "the job panicked")
		}
	}()

	return fn(ctx)
}
