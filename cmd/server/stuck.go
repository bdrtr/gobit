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
	"time"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/workflow"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// stuckCommand is the subcommand that lists the executions needing a human.
//
// # Why a command and not an endpoint
//
// The rows belong to the workflow engine, which is CORE, and core does not own
// HTTP endpoints the way a module does: an admin endpoint over them would have
// to be published by some module's api package, and there is no module that
// owns the saga engine. The admin panel was the other candidate and was
// rejected for a rule the panel's own ADR writes down — ADR 0011 reopens its
// read decision at "the first moment a panel screen can do something the
// framework's API does not offer", and a screen over core's execution tables is
// exactly that moment.
//
// A command has none of that weight and one property the other two cannot
// match: it needs no new identity surface. It runs with the server's own
// environment and reaches the database with the same credential psql already
// needs today, so it publishes the execution inputs — which carry cart
// contents — to nobody who could not already read the table.
//
// The shape is the one this repository has already written down as the remedy
// for a gap of the same class: README and docs/mimari.md both name a
// "cmd/server migrate subcommand" as the exit for `db.MigrateDown` being
// callable from Go and from nowhere else.
const stuckCommand = "stuck"

// defaultStuckLimit bounds the page when the operator gives no -limit.
//
// Fifty is a page a human reads, not a number the database cares about: the
// listing costs 13.8 ms against 52,000 executions either way (measured, best of
// five, local Postgres). It is a DEFAULT and not a cap — the flag raises it —
// and whether it was hit is printed on the last line, because a listing that
// quietly stopped at fifty would report a smaller incident than the real one.
const defaultStuckLimit = 50

// stuckLister is the one capability the command needs from the store.
//
// Declared on the consumer side (ADR 0001) so the printing can be exercised
// without a database: everything below the query is formatting, and formatting
// bugs are what an operator reads.
type stuckLister interface {
	Stuck(ctx context.Context, filter pgstore.StuckFilter) (pgstore.StuckPage, error)
}

// runStuck parses the flags, opens the database and prints the listing.
//
// It reuses [config.Load], so it needs the SAME environment as the server —
// which is the point: run inside the running container, it is configured
// already and cannot be pointed at the wrong database by accident. Nothing here
// migrates, opens Redis, or starts a listener.
//
// The context is built HERE rather than handed down, from the same signals the
// server watches: an operator who hits Ctrl-C during an incident expects the
// query to stop, and a background context would leave it running until the
// database answered.
func runStuck(args []string, out io.Writer) error {
	filter, err := parseStuckFlags(args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		// The flag set has already printed the usage. Asking what a command does
		// is not a failure, and returning the error here would answer the
		// question with a non-zero exit and the word "fatal".
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

	// The pool's own log goes to STDERR, not stdout: stdout is the operator's
	// data and a log line landing in the middle of it would break the first
	// grep. The level is Warn so that the case-folding probe still gets to
	// speak — that warning is about the same database this command is reading.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	pool, err := db.New(ctx, dbConfig(cfg), log)
	if err != nil {
		return err
	}
	defer pool.Close()

	return listStuck(ctx, pgstore.NewReader(pool), out, filter, time.Now().UTC())
}

// listStuck reads one page and prints it.
//
// The split from [runStuck] is what makes the output testable: everything below
// the query is formatting, and the formatting is what an operator actually
// reads during an incident.
func listStuck(
	ctx context.Context,
	lister stuckLister,
	out io.Writer,
	filter pgstore.StuckFilter,
	now time.Time,
) error {
	page, err := lister.Stuck(ctx, filter)
	if err != nil {
		return err
	}

	return writeStuck(out, page, filter, now)
}

// parseStuckFlags turns the command line into a listing filter.
//
// The two bounds carry DEFAULTS here and nowhere below: [pgstore.StuckFilter]
// rejects a zero value for either, so the number the operator sees printed is
// the number that reached the query — there is no second place where a missing
// value could quietly become something else.
func parseStuckFlags(args []string) (pgstore.StuckFilter, error) {
	flags := flag.NewFlagSet("gobit "+stuckCommand, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		fmt.Fprintf(os.Stderr,
			"usage: gobit %s [flags]\n\n"+
				"Lists workflow executions that were left half-done and need a human.\n"+
				"READ ONLY: it never releases stock, closes an execution or frees an\n"+
				"idempotency key. Releasing a reservation while its saga is still running\n"+
				"reserves the stock a second time.\n\n"+
				"flags:\n", stuckCommand)
		flags.PrintDefaults()
	}

	name := flags.String("workflow", "",
		"list only this workflow; empty lists every workflow")
	staleAfter := flags.Duration("stale-after", checkoutwf.ExecutionLease,
		"how long a 'running' execution may go untouched before it counts as abandoned. "+
			"Must be at least the lease the workflow declares; shorter and the listing "+
			"names sagas that are still running")
	limit := flags.Int("limit", defaultStuckLimit,
		"maximum executions to print; the last line says whether the cap was reached")

	if err := flags.Parse(args); err != nil {
		return pgstore.StuckFilter{}, err
	}
	if flags.NArg() > 0 {
		flags.Usage()

		return pgstore.StuckFilter{}, fmt.Errorf("gobit %s takes no positional arguments, got %q",
			stuckCommand, flags.Arg(0))
	}

	return pgstore.StuckFilter{
		Workflow:   *name,
		StaleAfter: *staleAfter,
		Limit:      *limit,
	}, nil
}

// writeStuck prints a page in the shape an operator reads during an incident.
//
// The layout answers, in order, the two questions the README says have no
// surface today: WHICH executions are stuck, and WHAT is still held. The held
// steps are marked and their outputs printed verbatim, because the reservation
// ids live in that JSON and the saga keeps them readable after compensation for
// exactly this moment.
//
// now is a parameter so the ages are reproducible in a test; taking it from the
// clock inside would make the output untestable line for line.
func writeStuck(out io.Writer, page pgstore.StuckPage, filter pgstore.StuckFilter, now time.Time) error {
	buf := &strings.Builder{}

	fmt.Fprintf(buf, "gobit %s: executions left half-done. READ ONLY — nothing below was modified.\n",
		stuckCommand)
	fmt.Fprintf(buf, "workflow=%s  stale-after=%s  limit=%d  running counts as abandoned before %s\n\n",
		stuckWorkflowLabel(filter.Workflow), filter.StaleAfter, page.Limit,
		page.StaleBefore.Format(time.RFC3339))

	held := 0
	for _, exec := range page.Executions {
		fmt.Fprintf(buf, "%s  %s  %s  idle=%s\n",
			exec.ID, exec.Workflow, stuckStatusLabel(exec.Status),
			now.Sub(exec.UpdatedAt).Truncate(time.Second))
		fmt.Fprintf(buf, "  idempotency_key: %s\n", stuckValueLabel(exec.IdempotencyKey))
		fmt.Fprintf(buf, "  input:           %s\n", stuckValueLabel(string(exec.Input)))
		if exec.Failure != "" {
			fmt.Fprintf(buf, "  failure:         %s\n", exec.Failure)
		}
		// Indexed rather than ranged by value: a StepRecord is 136 bytes and the
		// loop only reads.
		for i := range exec.Steps {
			rec := &exec.Steps[i]
			marker := "     "
			if rec.Status.Held() {
				marker = "HELD "
				held++
			}
			fmt.Fprintf(buf, "  %sstep %d %s (%s)\n", marker, rec.Index, rec.Name, rec.Status)
			if rec.Status.Held() && len(rec.Output) > 0 {
				fmt.Fprintf(buf, "         %s\n", rec.Output)
			}
		}
		buf.WriteString("\n")
	}

	fmt.Fprintf(buf, "%d execution(s), %d held step(s).\n", len(page.Executions), held)
	if page.Truncated {
		fmt.Fprintf(buf,
			"THE LIST IS INCOMPLETE: more executions matched than -limit=%d allowed. "+
				"Raise -limit to see the rest.\n", page.Limit)
	}
	if held > 0 {
		fmt.Fprintf(buf,
			"A held step's side effect is still in the world; its output above names what. "+
				"This command will not undo it. A caller returning with the same "+
				"idempotency key may recover it automatically (ADR 0017); an execution "+
				"nobody returns to, or one stopped at the payment step, needs a human.\n")
	}

	_, err := io.WriteString(out, buf.String())

	return err
}

// stuckWorkflowLabel renders the workflow filter.
//
// An empty filter is printed as a word rather than as nothing: a blank after
// "workflow=" reads like a failed lookup, when it actually means the widest
// possible listing.
func stuckWorkflowLabel(name string) string {
	if name == "" {
		return "<all>"
	}

	return name
}

// stuckValueLabel renders a field that may legitimately be empty.
func stuckValueLabel(value string) string {
	if value == "" {
		return "<none>"
	}

	return value
}

// stuckStatusLabel spells out what the status means for the reader.
//
// The two listed statuses are not the same problem and the difference decides
// what the operator does next. A compensation_failed record was CLOSED by the
// engine and logged at ERROR, so it is already known. A running record past its
// lease was noticed by NOTHING, and that is not the same sentence: the engine
// reaches compensation_failed either live (a step and its compensation both
// fail in one call) or on the replay path (an expired lease is judged), and a
// process that died mid-saga with a shopper who never came back triggers
// neither. Printing the bare status would leave that distinction in a godoc
// where the operator is not looking.
func stuckStatusLabel(status workflow.Status) string {
	if status == workflow.StatusRunning {
		return string(status) + " (abandoned; nothing has reported this one)"
	}

	return string(status)
}
