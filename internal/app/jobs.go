package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errorreport"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/eventbus/outbox"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/job"
	"github.com/bdrtr/gobit/internal/core/job/jobpg"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
	"github.com/bdrtr/gobit/internal/jobs/outboxrelay"
	"github.com/bdrtr/gobit/internal/jobs/paymentrecon"
	"github.com/bdrtr/gobit/internal/jobs/sagawatch"
	"github.com/bdrtr/gobit/internal/modules/payment"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
)

// paymentReconciler is the payment service as the reconciliation job needs it.
//
// The service is taken through a NARROW interface rather than by its concrete
// type, the same way seedAdmin takes the auth service: the composition root
// states what a dependency is FOR, and a root that resolved whole services
// would make every job look like it could do anything the module can.
type paymentReconciler interface {
	Reconcile(
		ctx context.Context, unchangedFor time.Duration, limit int,
	) (paymentsvc.ReconciliationReport, error)
}

// jobsCommand is the subcommand that prints the job listing.
const jobsCommand = "jobs"

// registerJobs declares the jobs this binary runs.
//
// Jobs are declared at the COMPOSITION ROOT, exactly as modules are. A plugin
// may now add one as well ([coreplugin.Host.RegisterJob]), and that is still the
// composition root doing it: the plugin DECLARES during Setup, this function
// admits, and the scheduler never sees anything the root did not hand it.
//
// The three in-repo jobs were pre-authorized rather than invented. ADR 0016
// built the operator's read surface for half-finished sagas and left the
// alerting half explicitly unclaimed ("It is a snapshot, not an alert. Nobody
// is told a cart is stuck") — that gap is internal/jobs/sagawatch. ADR 0019
// recorded payment reconciliation as the repository's one unkept periodic
// promise, named by internal/workflows/checkout/doc.go as the only correct way
// to close a known hole — that is internal/jobs/paymentrecon. The third is the
// outbox relay, which is the delivery half of the transactional outbox.
//
// Two of the three only read and report. The relay is the exception and it is
// worth naming rather than glossing: it publishes and it marks rows, so the
// scheduler DOES write. What ADR 0017 refuses is narrower than "writing" — it
// refuses running COMPENSATIONS, side effects that undo work, on a schedule
// nobody watched. Sending a message that a committed transaction already
// promised to send is not that.
func registerJobs(
	c *container.Container, host *coreplugin.Host, log *slog.Logger,
) (*job.Registry, error) {
	registry := job.NewRegistry()

	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindOf(err), job.CodeInvalidDefinition,
			"the job runner could not resolve the database pool (%q)", svcDB)
	}

	if err := registry.Add(sagawatch.Definition(pgstore.NewReader(pool), log)); err != nil {
		return nil, err
	}

	// The payment service is resolved from the container, so this fails loudly
	// if the module is not registered. A job silently left out of the registry
	// would be worse than a boot failure: `gobit jobs` would show a listing
	// with nothing missing from it, and an operator would read "no
	// reconciliation has run" as "there was nothing to reconcile".
	recon, err := container.Resolve[paymentReconciler](c, payment.ServiceName)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindOf(err), job.CodeInvalidDefinition,
			"the job runner could not resolve the payment service (%q)", payment.ServiceName)
	}

	if err := registry.Add(paymentrecon.Definition(recon, log)); err != nil {
		return nil, err
	}

	// The outbox relay is the delivery half of the transactional outbox: the
	// modules write the event with their work, this sends it. It is registered
	// unconditionally, because an installation whose relay is missing looks
	// exactly like one whose subscribers are slow.
	bus, err := container.Resolve[eventbus.EventBus](c, svcEventBus)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindOf(err), job.CodeInvalidDefinition,
			"the job runner could not resolve the event bus (%q)", svcEventBus)
	}

	if err := registry.Add(outboxrelay.Definition(outbox.NewStore(pool.Pool()), bus, log)); err != nil {
		return nil, err
	}

	// The plugins go in LAST, and the order is the error message. The registry
	// refuses a duplicate name, so whichever side is added second is the one
	// that fails — and the operator can change a plugin's job name far more
	// easily than one of the core's, whose name is the primary key of a history
	// they already have.
	if err := addPluginJobs(registry, host); err != nil {
		return nil, err
	}

	return registry, nil
}

// addPluginJobs admits the jobs the plugins declared during Setup.
//
// # The translation, and why it is here
//
// A plugin cannot name [job.Definition]: it is internal, and a plugin outside
// this module could not declare one (see [coreplugin.Job]). So the plugin fills
// in a published four-field struct and this is the single place that turns it
// into the scheduler's own. One place, at the boundary, rather than a shape the
// scheduler has to keep accepting forever.
//
// # Nothing is skipped, and nothing is validated twice
//
// A definition that cannot run is refused by [job.Registry.Add] — the very same
// call, and therefore the very same rule, that the three jobs above go through.
// A MaxRun longer than the interval fails the boot; so does a missing Run, an
// empty name and a name already taken. Skipping such a job instead would be the
// worst outcome available: `gobit jobs` would print a listing with nothing
// missing from it, and an operator reading it would take the absence of the
// plugin's line as "that pass had nothing to do".
//
// The error is re-wrapped only to name the plugin. The original code is carried
// through rather than replaced, because "duplicate name" and "invalid
// definition" send whoever reads it to two different places.
func addPluginJobs(registry *job.Registry, host *coreplugin.Host) error {
	if host == nil {
		// The migrate subcommand builds a host it throws away, and a nil host
		// is how a test says "no plugins". Neither is a reason to fail.
		return nil
	}

	for _, declared := range host.Jobs() {
		definition := job.Definition{
			Name:   declared.Name,
			Every:  declared.Every,
			MaxRun: declared.MaxRun,
			Run:    job.Func(declared.Run),
		}

		if err := registry.Add(definition); err != nil {
			return coreerrors.Wrap(err, coreerrors.KindOf(err), coreerrors.CodeOf(err),
				"the %q job declared by the %s plugin was refused",
				declared.Name, declared.PluginName())
		}
	}

	return nil
}

// startJobs builds the runner and starts it.
//
// The returned stop function is called on the way out of serve; it waits for
// the pass in progress so that shutdown cannot close the pool underneath a job
// that is still writing its outcome.
func startJobs(
	ctx context.Context,
	c *container.Container,
	host *coreplugin.Host,
	cfg config.Config,
	log *slog.Logger,
) (*job.Runner, func(), error) {
	registry, err := registerJobs(c, host, log)
	if err != nil {
		return nil, nil, err
	}

	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return nil, nil, err
	}

	runner, err := job.New(job.Options{
		Registry: registry,
		Store:    jobpg.New(pool),
		Logger:   log,
	})
	if err != nil {
		return nil, nil, err
	}

	warnIfAJobOutlivesShutdown(ctx, registry, cfg, log)

	runner.Start(ctx)

	return runner, runner.Stop, nil
}

// warnIfAJobOutlivesShutdown reports a job that can still be running when the
// process is told to stop.
//
// Such a job is not broken — its advisory lock dies with the backend and its
// occurrence row stays unfinished, which is exactly what the listing is for.
// What it is, is invisible: the operator sees a shutdown that took the full
// timeout and a job with no end recorded, with nothing connecting the two.
// This line connects them, once, at startup, before it happens.
func warnIfAJobOutlivesShutdown(
	ctx context.Context, registry *job.Registry, cfg config.Config, log *slog.Logger,
) {
	for _, d := range registry.Definitions() {
		if d.MaxRun <= cfg.ShutdownTimeout {
			continue
		}
		log.WarnContext(ctx, "a job may still be running when the process is asked to stop",
			"job", d.Name,
			"max_run", d.MaxRun.String(),
			"shutdown_timeout", cfg.ShutdownTimeout.String())
	}
}

// runJobs prints what each job is and when it last ran.
//
// It is the answer to the first question an operator asks — "did it run last
// night?" — and it is a subcommand rather than an endpoint because it is asked
// from a terminal during an incident, when the admin API may be the thing that
// is broken.
func runJobs(args []string, out io.Writer, opts Options) error {
	if len(args) > 0 {
		return coreerrors.Invalid(job.CodeUnknown, "jobs takes no arguments")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The pool's own log goes to STDERR, not stdout: stdout is the operator's
	// data and a log line landing in the middle of it would break the first
	// grep. This is the same split runStuck makes, for the same reason.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// The WHOLE application is opened, not just a pool, and the reason is that
	// [registerJobs] is the same function the runner calls. A job whose
	// dependency is a module can only be built from a container that has the
	// modules in it, and a listing built from a thinner container than the
	// runner's would quietly describe a different set of jobs than the one that
	// actually runs — which is the one thing this command exists not to do.
	//
	// The same reasoning now covers the plugins, and it costs nothing extra:
	// opening the application runs their Setup, and a job is DECLARED there. So
	// this listing carries a plugin's jobs even though it never reaches the
	// Start phase that applies the providers and the subscriptions.
	//
	// Nothing is started here: opening builds and bootstraps, and the runner
	// lives in serve (see [startJobs]).
	app, closeApp, err := openApplication(ctx, cfg, log, errorreport.NewSink(), opts)
	if err != nil {
		return err
	}
	defer closeApp()

	registry, err := registerJobs(app.container, app.host, log)
	if err != nil {
		return err
	}

	pool, err := container.Resolve[*db.Pool](app.container, svcDB)
	if err != nil {
		return err
	}

	store := jobpg.New(pool)

	names := make([]string, 0, registry.Len())
	for _, d := range registry.Definitions() {
		names = append(names, d.Name)
	}

	history, err := store.Last(ctx, names)
	if err != nil {
		return err
	}

	return printJobs(out, registry.Definitions(), history, time.Now().UTC())
}

// printJobs renders the listing.
func printJobs(
	out io.Writer, defs []job.Definition, history map[string]job.Run, now time.Time,
) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)

	if _, err := fmt.Fprintln(w, "JOB\tEVERY\tLAST RUN\tOUTCOME\tDETAIL"); err != nil {
		return err
	}

	for _, d := range defs {
		last, known := history[d.Name]

		when, outcome := "never", "-"
		switch {
		case !known:
			// A job that has never run is not necessarily broken — the process
			// may have started minutes ago — but it is the one row an operator
			// must not read as "fine".
		case last.Unfinished():
			when = last.StartedAt.Format(time.RFC3339)
			// The record cannot tell running from died; the LOCK can, and the
			// listing says so rather than guessing.
			outcome = "unfinished (running now, or the process died)"
		case last.Succeeded():
			when = last.EndedAt.Format(time.RFC3339)
			outcome = "ok"
		default:
			when = last.EndedAt.Format(time.RFC3339)
			outcome = "FAILED: " + last.Failure
		}

		overdue := ""
		if known && !last.Due.IsZero() && now.Sub(last.Due) > 2*d.Every {
			// Two intervals, not one: a job that just became due has not run
			// yet by definition, and calling that overdue would make the column
			// meaningless.
			overdue = "  (OVERDUE)"
		}

		if _, err := fmt.Fprintf(w, "%s\t%s\t%s%s\t%s\t%s\n",
			d.Name, d.Every, when, overdue, outcome, last.Detail); err != nil {
			return err
		}
	}

	return w.Flush()
}
