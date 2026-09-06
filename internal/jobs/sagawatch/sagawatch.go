// Package sagawatch reports sagas that died half-finished, and never touches
// them.
//
// # What it watches, and why that class and not the other
//
// ADR 0016 built `gobit stuck` and measured what it finds: of six executions
// covering every terminal state, two need a human, and the one-line status
// query finds one of the two. The one it misses is the class this job exists
// for — an execution still marked "running", untouched since its lease, holding
// at least one step whose side effect is still in the world.
//
// pgstore/stuck.go states the consequence plainly: "that record stays running
// forever, holds stock, and is mentioned by nothing: no log line, no metric, no
// status." Held work is reserved inventory. Nobody can buy it and nobody is
// told, so it is a correctness cost rather than an ergonomic one.
//
// It deliberately does NOT watch StatusCompensationFailed. That status is the
// engine's own statement that a human is needed, and reaching it required
// something to happen IN PROCESS — the engine wrote it and logged it at ERROR
// at the same moment. Re-reporting it every hour would train an operator to
// ignore the line that carries the class nothing else reports.
//
// # It never acts, and that is the decision it lives inside
//
// ADR 0017 refuses a scheduled sweeper in four places, and the refusal is
// precise: recovery runs COMPENSATIONS, which are side effects, and a scheduled
// job would decide on its own, unwatched, to undo work. Nothing here undoes
// anything. It counts, it logs, and it stops.
//
// The repair is still `gobit recover <execution-id> -confirm`, run by a person
// who looked. What changes is that the person now learns there is something to
// look at.
//
// # Where "the person learns" actually happens
//
// It used to be one log line and nothing else, which is a weaker claim than the
// paragraph above makes. A job that succeeds could not leave a line in
// `gobit jobs` at all — the runner recorded a detail only alongside an error —
// so the listing an operator opens FIRST showed "ok" and an empty cell for an
// hour in which this job found abandoned sagas holding stock. [job.Report]
// closes that, and the count now appears in the listing beside the run.
//
// The pass still succeeds when it finds one. Failing would put the row in the
// OUTCOME column, which is louder, and louder is not obviously right here: what
// this finds has been stuck for longer than a lease already and the repair is a
// deliberate human command, so a standing failure would sit there for as long
// as the operator takes to decide — training them to read past it. That is a
// judgement, not a rule, and it is written down so the next person changes it
// on purpose.
package sagawatch

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/job"
	"github.com/bdrtr/gobit/internal/core/workflow"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
)

// Name is the job's name. It is the advisory lock's input and the primary key
// of its history, so it is a contract.
const Name = "saga-watch"

// Every is how often the watch runs.
//
// Hourly rather than by the minute: what it reports is work that has ALREADY
// been stuck for longer than a lease, so finding it a few minutes sooner
// changes nothing, while an hourly line is one an operator can still read.
const Every = time.Hour

// MaxRun bounds one pass.
//
// The query is one indexed scan with a correlated EXISTS; on a
// 52,000-execution fixture it was measured at 2.9 ms without the JIT. A minute
// is not a budget, it is a tripwire: a pass that takes longer means something
// about the shape of the data changed.
const MaxRun = time.Minute

// staleAfter is how long an execution may sit in "running" before this job
// treats it as abandoned.
//
// It is the same quantity the engine calls a lease, and StuckFilter's own
// documentation gives the rule: set it SHORTER than the workflow's lease and
// the listing names sagas that are still running. An operator who then released
// their stock by hand would double-free inventory the saga is about to
// compensate.
//
// An hour is deliberately far above the checkout lease that `gobit stuck`
// defaults to (ten minutes). This job reports without being asked, so a false
// positive here costs more than a late true one: the first thing an unnecessary
// alert teaches is that the alert can be ignored.
const staleAfter = time.Hour

// limit caps one pass.
//
// A cap that is hit is REPORTED as hit rather than silently truncating: an
// incident that produced two hundred stuck sagas must not be described as
// fifty.
const limit = 200

// codeWatchFailed reports that the listing could not be read.
const codeWatchFailed = "sagawatch_read_failed"

// reader is the narrow surface this job needs.
//
// It is declared HERE rather than taken from pgstore, so that the job depends
// on the one method it uses and a test can supply it without a database.
type reader interface {
	Stuck(ctx context.Context, filter pgstore.StuckFilter) (pgstore.StuckPage, error)
}

// Definition builds the job.
func Definition(r reader, log *slog.Logger) job.Definition {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return job.Definition{
		Name:   Name,
		Every:  Every,
		MaxRun: MaxRun,
		Run:    func(ctx context.Context) error { return run(ctx, r, log) },
	}
}

// run reads the listing and reports what nobody else would.
func run(ctx context.Context, r reader, log *slog.Logger) error {
	page, err := r.Stuck(ctx, pgstore.StuckFilter{StaleAfter: staleAfter, Limit: limit})
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeWatchFailed,
			"the stuck saga listing could not be read")
	}

	abandoned := abandonedOnly(page.Executions)

	// The count reaches `gobit jobs`, which is the listing an operator opens
	// before they open a log. It is reported on the quiet passes too: "0
	// abandoned" is the sentence that makes the hour it becomes 1 legible.
	job.Report(ctx, summarize(len(abandoned), page.Truncated))

	if len(abandoned) == 0 {
		// Logged at DEBUG rather than INFO. A healthy installation runs this
		// every hour forever, and a line that is always the same is a line
		// nobody reads — which is how the one that differs gets missed.
		log.DebugContext(ctx, "no abandoned saga is holding work",
			"stale_after", staleAfter.String())

		return nil
	}

	// ERROR, and the severity is the point: every one of these is holding
	// inventory that no customer can buy, and until this job existed nothing in
	// the process mentioned them at all.
	log.ErrorContext(ctx, "abandoned sagas are holding work and need a human; "+
		"list them with `gobit stuck` and undo one with `gobit recover <id> -confirm`",
		"abandoned", len(abandoned),
		"truncated", page.Truncated,
		"stale_before", page.StaleBefore,
		// The IDs are the operator's next command. They are execution ids, not
		// business identifiers: no cart, customer or order id is logged.
		"execution_ids", idsOf(abandoned))

	return nil
}

// summarize renders one pass as the single line `gobit jobs` prints.
//
// The execution ids stay in the LOG and do not come here. The listing's cell is
// one column of a table an operator scans; a run that found twenty abandoned
// sagas would push every other job's row off the terminal, and the ids are
// useless without the next command anyway — which is `gobit stuck`, and which
// prints them.
func summarize(abandoned int, truncated bool) string {
	line := fmt.Sprintf("%d abandoned saga(s) holding work", abandoned)
	if truncated {
		line += fmt.Sprintf("; the limit of %d was filled, so there may be more", limit)
	}

	return line
}

// abandonedOnly keeps the class nothing else reports.
//
// StuckFilter returns two classes in one page — the engine's own
// compensation_failed verdict, and executions still marked running past their
// lease. Only the second is this job's business; the first was already written
// and logged at ERROR by the engine when it happened.
func abandonedOnly(executions []*workflow.Execution) []*workflow.Execution {
	out := make([]*workflow.Execution, 0, len(executions))
	for _, e := range executions {
		if e == nil || e.Status != workflow.StatusRunning {
			continue
		}
		out = append(out, e)
	}

	return out
}

// idsOf lists the execution ids for the log line.
func idsOf(executions []*workflow.Execution) []string {
	out := make([]string, 0, len(executions))
	for _, e := range executions {
		out = append(out, e.ID)
	}

	return out
}
