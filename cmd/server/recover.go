package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errorreport"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/workflow"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// recoverCommand is the subcommand that compensates a half-done saga.
//
// # Why a command, and why it WRITES
//
// `gobit stuck` reads: it lists the executions that need a human and releases
// nothing, because releasing the stock of a saga that is still running would
// reserve it twice. That leaves the operator holding a list they cannot act on.
//
// The engine's own recovery is ARRIVED AT: it runs when a caller returns with
// the same idempotency key (see [workflow.WithLease]). That covers the customer
// who retries and nothing else. An abandoned cart has no such caller, so the
// record stays running, the stock it reserved stays reserved and `gobit stuck`
// keeps listing it forever.
//
// This command is the hand that acts on that list. It is deliberately NOT a
// sweeper: a scheduled job would decide on its own, unwatched, to undo work
// whose side effects are real. Here a human names one execution.
//
// The shape follows `migrate down`, the other command in this binary that
// cannot be undone: nothing happens unless -confirm repeats the id.
const recoverCommand = "recover"

// runRecover parses the flags, brings the application up and runs the
// compensation chain of one execution.
//
// It reuses [config.Load] and [openApplication], so it needs the SAME
// environment as the server and reaches the same database — which is the point:
// run inside the running container, it is configured already and cannot be
// pointed at the wrong installation by accident. Nothing is served: no listener
// is opened and the plugins' queued registrations are not applied (see
// [openApplication]).
func runRecover(args []string, out io.Writer) error {
	flags, err := parseRecoverFlags(args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		// The flag set has already printed the usage. Asking what a command
		// does is not a failure.
		return nil
	case err != nil:
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Every log line goes to STDERR: stdout carries what the operator reads
	// back, and a boot line landing in the middle of it would break the first
	// grep. Warn keeps the startup checks that warn about THIS installation
	// (a shutdown budget shorter than the saga, a missing case-folding index)
	// visible, because the operator is about to act on that same installation.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	app, closeApp, err := openApplication(ctx, cfg, log, errorreport.NewSink())
	if err != nil {
		return err
	}
	defer closeApp()

	return recoverExecution(ctx, app.container, out, flags.executionID)
}

// recoverExecution reads the record, builds the definition for its workflow and
// runs the compensation chain.
//
// The split from [runRecover] is what makes it testable without a process:
// everything above it is the environment, everything here is the decision.
func recoverExecution(ctx context.Context, c *container.Container, out io.Writer, executionID string) error {
	store, err := container.Resolve[workflow.Store](c, svcWorkflowStore)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeRecoverFailed,
			"the workflow store %q could not be resolved", svcWorkflowStore)
	}

	engine, err := container.Resolve[workflow.Executor](c, svcWorkflow)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeRecoverFailed,
			"the workflow engine %q could not be resolved", svcWorkflow)
	}

	recoverer, ok := engine.(workflow.Recoverer)
	if !ok {
		return errors.Internal(codeRecoverFailed,
			"the workflow engine does not offer the recovery capability")
	}

	exec, err := store.Get(ctx, executionID)
	if err != nil {
		return err
	}

	wf, err := recoveryWorkflowFor(c, exec)
	if err != nil {
		return err
	}

	if err := writeReport(out, fmt.Sprintf("recovering %s (%s, key %q)\n",
		exec.ID, exec.Workflow, exec.IdempotencyKey)); err != nil {
		return err
	}

	if err := recoverer.Recover(ctx, wf, exec.ID, checkoutwf.RecoveryOptions()...); err != nil {
		return err
	}

	after, err := store.Get(ctx, exec.ID)
	if err != nil {
		return err
	}

	// The released key is stated rather than left to be inferred: it is the
	// half the customer feels — the cart can be paid for again — and an
	// operator reading only "failed" would not know it happened.
	released := ""
	if after.IdempotencyKey == "" {
		released = ", its idempotency key was released"
	}

	return writeReport(out, fmt.Sprintf("done: %s is now %s%s\n", after.ID, after.Status, released))
}

// recoveryWorkflowFor builds the definition of the workflow the record belongs
// to, FROM the record's own input.
//
// The definition is needed for its Compensate functions and its step names, and
// the plan those steps were built from died with the process. It is rebuilt
// here from the input the engine persisted (see
// [checkoutwf.Workflows.RecoveryWorkflow]).
//
// A workflow this binary does not know is REFUSED by name rather than guessed
// at: a compensation chain built from the wrong definition undoes the wrong
// work.
func recoveryWorkflowFor(c *container.Container, exec *workflow.Execution) (workflow.Workflow, error) {
	if exec.Workflow != checkoutwf.WorkflowName {
		return workflow.Workflow{}, errors.Invalid(codeRecoverFailed,
			"the execution %s belongs to the %q workflow; this command can only recover %q",
			exec.ID, exec.Workflow, checkoutwf.WorkflowName)
	}

	flows, err := checkoutwf.FromContainer(c)
	if err != nil {
		return workflow.Workflow{}, errors.Wrap(err, errors.KindOf(err), codeRecoverFailed,
			"the checkout workflow could not be set up")
	}

	return flows.RecoveryWorkflow(exec.Input)
}

// codeRecoverFailed reports that the recovery could not be started. What the
// engine itself refuses carries the engine's own codes (see
// [workflow.Recoverer]).
const codeRecoverFailed = "recover_failed"

// recoverFlags is the parsed command line.
type recoverFlags struct {
	executionID string
}

// parseRecoverFlags turns the command line into an execution id, and refuses
// everything that is not one.
//
// # Why a confirmation
//
// Recovery WRITES: it calls the flow's own Compensate functions, which release
// reservations, cancel the order and void the authorization. None of that can be
// taken back, and the id is a value an operator copies out of a `gobit stuck`
// listing — one line up or down is a different customer's saga. Repeating the id
// makes the copy deliberate.
//
// The engine's own refusals are the second gate and they are not overridable
// here: a live lease, a terminal record and an unrecorded payment step all stop
// the run no matter what is typed (see [workflow.Recoverer]).
func parseRecoverFlags(args []string) (recoverFlags, error) {
	// The id is required to be the FIRST argument, exactly as the owner is for
	// `migrate down` (see parseDownArgs): the flag package stops at the first
	// non-flag argument, so an id written after the flags would be swallowed as
	// a leftover.
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return recoverFlags{}, errors.Invalid(codeRecoverFailed,
			"%s needs the execution id as its FIRST argument (gobit %s <execution-id> -%s <execution-id>); find it with `gobit %s`",
			recoverCommand, recoverCommand, flagConfirm, stuckCommand)
	}

	flags := flag.NewFlagSet("gobit "+recoverCommand, flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	confirm := flags.String(flagConfirm, "",
		"repeat the execution id; nothing runs without it")

	if err := flags.Parse(args[1:]); err != nil {
		return recoverFlags{}, errors.Wrap(err, errors.KindInvalid, codeRecoverFailed,
			"the flags of %s could not be parsed", recoverCommand)
	}
	if rest := flags.Args(); len(rest) > 0 {
		return recoverFlags{}, errors.Invalid(codeRecoverFailed,
			"unexpected argument %q after the flags of %s", rest[0], recoverCommand)
	}

	id := args[0]
	if *confirm != id {
		return recoverFlags{}, errors.Invalid(codeRecoverFailed,
			"recovery cannot be undone: run it again with -%s %s to confirm", flagConfirm, id)
	}

	return recoverFlags{executionID: id}, nil
}
