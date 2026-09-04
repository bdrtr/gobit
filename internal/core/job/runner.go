package job

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// tickInterval is how often the runner looks for due occurrences.
//
// It is NOT the schedule. A job's interval decides which occurrence is due; the
// tick only decides how soon after an occurrence becomes due somebody notices.
// A minute is short enough that nothing waits long and long enough that an
// idle installation is not asking the database anything worth measuring.
const tickInterval = time.Minute

// Runner ticks and runs whatever is due.
//
// It is started once per process. Every instance runs one, and the Store's
// election is what stops them all doing the same work.
type Runner struct {
	registry *Registry
	store    Store
	log      *slog.Logger
	now      func() time.Time
	tick     time.Duration

	// running guards against a second Start.
	running sync.Mutex
	stop    context.CancelFunc
	done    chan struct{}
}

// Options are the runner's dependencies.
type Options struct {
	Registry *Registry
	Store    Store
	Logger   *slog.Logger
	// Now is injectable so a test can drive the clock; nil means time.Now.
	Now func() time.Time
	// Tick overrides [tickInterval]; zero means the default.
	Tick time.Duration
}

// New builds a runner.
func New(opts Options) (*Runner, error) {
	if opts.Registry == nil || opts.Store == nil {
		return nil, coreerrors.Internal(CodeInvalidDefinition,
			"a job runner needs both a registry and a store")
	}

	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	tick := opts.Tick
	if tick <= 0 {
		tick = tickInterval
	}

	return &Runner{registry: opts.Registry, store: opts.Store, log: log, now: now, tick: tick}, nil
}

// Start begins ticking in the background.
//
// It returns immediately. Starting with no jobs registered is not an error and
// not silent: the line says so, because "the scheduler is running" and "the
// scheduler has anything to do" are different facts and an operator reading the
// first should not infer the second.
func (r *Runner) Start(ctx context.Context) {
	r.running.Lock()
	defer r.running.Unlock()

	if r.done != nil {
		return
	}

	if r.registry.Len() == 0 {
		r.log.InfoContext(ctx, "the job runner started with no jobs registered")
	} else {
		r.log.InfoContext(ctx, "the job runner started",
			"jobs", r.names(), "tick", r.tick.String())
	}

	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r.stop = cancel
	r.done = make(chan struct{})

	go r.loop(ctx)
}

// Stop ends the loop and waits for the current pass to finish.
//
// It waits for the PASS, not for a running job: a job holds its own deadline
// (MaxRun) and the shutdown window is not the place to discover that somebody
// set it too high. What stops a job at shutdown is its context, which is
// derived from the runner's.
func (r *Runner) Stop() {
	r.running.Lock()
	defer r.running.Unlock()

	if r.done == nil {
		return
	}

	r.stop()
	<-r.done
	r.done = nil
}

// loop ticks until the context ends.
func (r *Runner) loop(ctx context.Context) {
	defer close(r.done)

	ticker := time.NewTicker(r.tick)
	defer ticker.Stop()

	// A pass runs immediately rather than after the first tick. A deploy that
	// restarts every instance would otherwise leave a window in which an
	// overdue job waits for no reason.
	r.pass(ctx)

	for {
		select {
		case <-ctx.Done():
			r.log.InfoContext(ctx, "the job runner stopped")

			return
		case <-ticker.C:
			r.pass(ctx)
		}
	}
}

// pass considers every job once.
func (r *Runner) pass(ctx context.Context) {
	for _, d := range r.registry.Definitions() {
		if ctx.Err() != nil {
			return
		}
		r.consider(ctx, d)
	}
}

// consider runs one job if its occurrence is due and unclaimed.
func (r *Runner) consider(ctx context.Context, d Definition) {
	due := Occurrence(d.Every, r.now())

	locked, err := r.store.WithLock(ctx, LockKey(d.Name), func(ctx context.Context) error {
		claimed, err := r.store.Claim(ctx, d.Name, due)
		if err != nil {
			return err
		}
		if !claimed {
			// Either another instance took this occurrence or it already ran.
			// Both mean the same thing here and neither is worth a log line
			// every tick on every replica.
			return nil
		}

		r.execute(ctx, d, due)

		return nil
	})
	if err != nil {
		r.log.WarnContext(ctx, "a job could not be considered",
			"job", d.Name, "due", due, "error", err)

		return
	}
	if !locked {
		// Somebody else is running it right now. Normal, and silent for the
		// same reason.
		return
	}
}

// execute runs the job and records the outcome.
func (r *Runner) execute(ctx context.Context, d Definition, due time.Time) {
	runCtx, cancel := context.WithTimeout(ctx, d.MaxRun)
	defer cancel()

	started := r.now()
	err := safely(runCtx, d.Run)
	elapsed := r.now().Sub(started)

	outcome := Outcome{Err: err, Duration: elapsed}
	if reporter, ok := detailOf(err); ok {
		outcome.Detail = reporter
	}

	// The outcome is recorded on a context DETACHED from the run's. A job that
	// was cut off by shutdown still has to leave a record: without it the
	// listing says the job never ran, and the next start would run it again.
	recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer recordCancel()

	if finishErr := r.store.Finish(recordCtx, d.Name, due, outcome); finishErr != nil {
		r.log.ErrorContext(ctx, "a job's outcome could not be recorded; the listing will be wrong",
			"job", d.Name, "due", due, "error", finishErr)
	}

	if err != nil {
		// ERROR, not WARN: a job nobody watches failing quietly is the exact
		// state this package was built to make impossible.
		r.log.ErrorContext(ctx, "a job failed",
			"job", d.Name, "due", due, "duration", elapsed.String(), "error", err)

		return
	}

	r.log.InfoContext(ctx, "a job ran",
		"job", d.Name, "due", due, "duration", elapsed.String(), "detail", outcome.Detail)
}

// RunNow runs one job immediately, ignoring the schedule but NOT the lock.
//
// This is what `gobit job run` calls. It still takes the lock, because the
// operator running a job by hand while the scheduler is running it is exactly
// the collision the lock exists for — and it is likeliest during an incident,
// when somebody is impatient.
//
// It does NOT claim an occurrence: a hand-run is deliberately outside the
// schedule, and consuming the occurrence would make the scheduled run silently
// not happen.
func (r *Runner) RunNow(ctx context.Context, name string) error {
	d, err := r.registry.Get(name)
	if err != nil {
		return err
	}

	locked, err := r.store.WithLock(ctx, LockKey(d.Name), func(ctx context.Context) error {
		runCtx, cancel := context.WithTimeout(ctx, d.MaxRun)
		defer cancel()

		return safely(runCtx, d.Run)
	})
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), CodeRunFailed,
			"the %q job failed", name)
	}
	if !locked {
		return coreerrors.Conflict(CodeRunFailed,
			"the %q job is already running somewhere; it was NOT started a second time", name)
	}

	return nil
}

// names lists the registered job names for a log line.
func (r *Runner) names() []string {
	defs := r.registry.Definitions()
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Name)
	}

	return out
}

// detailReporter is implemented by an error that carries a one-line operator
// note alongside the failure.
type detailReporter interface{ JobDetail() string }

// detailOf reads the operator note off an error, if it carries one.
func detailOf(err error) (string, bool) {
	var reporter detailReporter
	if err != nil && errors.As(err, &reporter) {
		return reporter.JobDetail(), true
	}

	return "", false
}
