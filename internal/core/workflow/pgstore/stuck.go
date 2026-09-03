package pgstore

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// CodeFilterInvalid reports a listing filter that would answer a different
// question than the one asked. Callers may branch on it; the message changes,
// the code does not.
const CodeFilterInvalid = "workflow_stuck_filter_invalid"

// StuckFilter bounds one [Reader.Stuck] listing. Both bounds are REQUIRED.
//
// Neither has a default here, and that is the point. A zero StaleAfter would
// silently list executions that are still running, and a zero Limit would
// silently mean "everything"; both are caps an operator has to be able to see,
// so the value comes from the caller and the caller prints it.
type StuckFilter struct {
	// Workflow narrows the listing to one workflow name. Empty means every
	// workflow — the useful default during an incident, when the operator does
	// not yet know which flow broke.
	Workflow string

	// StaleAfter is how long a record may sit in "running" before it is
	// treated as abandoned.
	//
	// It is the same quantity the engine calls a lease (see
	// [workflow.WithLease]) and it must be at least as long as the lease the
	// workflow itself declares. Set it shorter and the listing names sagas that
	// are still running: an operator who then releases their stock by hand
	// double-frees inventory the saga is about to compensate — the exact
	// failure the lease design was built to avoid.
	//
	// A value of zero or less is REJECTED rather than defaulted, because the
	// safe value is a property of the workflow and this package does not know
	// which workflow is being listed.
	StaleAfter time.Duration

	// Limit caps how many executions are returned.
	//
	// Whether the cap was hit is reported back on [StuckPage.Truncated]; a
	// listing that quietly stopped at its limit would tell an operator the
	// incident is smaller than it is.
	Limit int
}

// StuckPage is one page of executions that need a human.
//
// The bounds that produced it travel WITH it: the caller prints them next to
// the rows, so the reader of the output can tell "there are two stuck carts"
// from "there are at least two, and I asked for at most two".
type StuckPage struct {
	// Executions carry their step records, oldest first. The step records are
	// the answer to "what is held": read [workflow.StepStatus.Held] over them.
	Executions []*workflow.Execution
	// Truncated reports that at least one more execution matched than Limit
	// allowed.
	Truncated bool
	// StaleBefore is the instant a running execution had to be untouched since
	// in order to be listed (now minus StaleAfter, as the database saw it).
	StaleBefore time.Time
	// Limit echoes [StuckFilter.Limit].
	Limit int
}

// Reader is the operator-facing READ surface over the execution tables.
//
// # Why it is a separate type and not a Store method
//
// [workflow.Store] is the contract the ENGINE consumes, and the engine never
// lists: adding a listing method there would force the in-memory store to grow
// an implementation nothing exercises. Keeping the surface here also keeps the
// promise below checkable by reading one small file.
//
// # It cannot write
//
// Every statement this type issues is a SELECT. That is deliberate and it is
// the whole reason the listing was built before any release button: releasing
// a reservation while its saga is still running reserves the stock a second
// time, and the lease design spends its entire godoc avoiding exactly that
// (see [workflow.WithLease]). Reading is safe at any moment; writing is not,
// and this type never has to decide which moment it is.
type Reader struct {
	pool *db.Pool
}

// NewReader returns a listing reader over the given pool.
//
// A nil pool is accepted and every method then fails with a typed
// KindUnavailable error, matching [New]'s contract: a constructor that cannot
// fail is easier to wire from a composition root than one that can.
func NewReader(pool *db.Pool) *Reader {
	return &Reader{pool: pool}
}

// selectStuckSQL selects the executions that need a human, with their steps.
//
// # The two classes, and why they are not the same condition
//
// $2 is the terminal status that means "work was done and could not be undone"
// ([workflow.StatusCompensationFailed]). Such a record was already CLOSED by
// the engine and already logged at ERROR; it is listed unconditionally,
// including when it has no held step left, because its status is the engine's
// own statement that a human is needed.
//
// $3/$4/$5 are the class nothing has noticed yet: still "running", untouched
// since $4, and carrying at least one step whose side effect is still in the
// world ($5, from [workflow.HeldStepStatuses]). This class matters more than
// the first one and it is the reason this query is not a one-line
// "WHERE status = 'compensation_failed'". Reaching that status needs SOMETHING
// TO HAPPEN, and the two things that can are both in-process: the engine writes
// it live in unwind() when a step fails and its compensation fails too, and it
// writes it on the replay path when an expired lease is judged. Neither fires
// for a process that DIED mid-saga while the shopper never came back — that
// record stays running forever, holds stock, and is mentioned by nothing: no
// log line, no metric, no status.
//
// # The planner over-estimates this query, and at volume that turns on the JIT
//
// The correlated EXISTS is costed as if it ran for every execution, though it
// only runs for the rows that already matched the status and the cutoff.
// Measured on a 52.000-execution fixture: estimated cost 440.056 against an
// actual 8 rows. That number matters for one reason — the default
// jit_above_cost is 100.000, so PostgreSQL compiles this query every time it is
// run. Also measured, same fixture: 22,9 ms with the JIT, 2,9 ms without it.
//
// It is left alone DELIBERATELY. Twenty milliseconds is nothing for a command a
// human types during an incident, and the two ways to remove it are both worse
// than the thing they remove: SET LOCAL jit = off needs a transaction around a
// read that does not otherwise need one, and rewriting the EXISTS into a join
// would trade a plan the planner gets right for one it might not. The number is
// written here so that the next person to see a 20 ms floor on a query that
// returns eight rows knows what it is and that it was a choice.
//
// The held-step condition applies only to the running class. A stale running
// record with nothing held holds no inventory and the engine repairs it by
// itself on the next attempt (it becomes 'failed' and releases its key); listing
// it would fill an incident page with rows that need no action.
//
// # Why the statuses are parameters and not literals
//
// Writing 'compensation_failed' into this string would make a second copy of a
// Go constant, and the two would drift. updateStatusSQL refuses the same thing
// for the same reason.
//
// # Ordering
//
// Oldest first, by updated_at. The oldest record is the one whose reservation
// has been held longest, so it is the one an operator should look at first;
// e.id breaks ties so a page is stable between two runs. The inner ORDER BY
// decides WHICH executions are on the page and the outer decides how they are
// printed — they must stay identical, or the page would be chosen by one order
// and shown in another.
const selectStuckSQL = `
WITH cutoff AS (
	SELECT now() - make_interval(secs => $4::double precision) AS stale_before
), selected AS (
	SELECT e.id, e.updated_at
	FROM workflow_executions e
	WHERE ($1 = '' OR e.workflow = $1)
	  AND (
		e.status = $2
		OR (
			e.status = $3
			AND e.updated_at < (SELECT stale_before FROM cutoff)
			AND EXISTS (
				SELECT 1
				FROM workflow_execution_steps s
				WHERE s.execution_id = e.id AND s.status = ANY($5)
			)
		)
	  )
	ORDER BY e.updated_at, e.id
	LIMIT $6
)
SELECT
	(SELECT stale_before FROM cutoff),
	e.id, e.workflow, e.idempotency_key, e.status, e.input, e.output, e.failure,
	e.created_at, e.updated_at,
	s.step_index, s.name, s.status, s.output, s.failure, s.attempts,
	s.started_at, s.ended_at
FROM selected
JOIN workflow_executions e ON e.id = selected.id
LEFT JOIN workflow_execution_steps s ON s.execution_id = e.id
ORDER BY selected.updated_at, selected.id, s.step_index`

// Stuck lists the executions that need a human, oldest first.
//
// It answers one question — "which executions are half-done, and what is still
// held?" — and it answers it by reading. Nothing here releases a reservation,
// closes an execution or frees an idempotency key; see [Reader] for why that
// separation is not a matter of scope but of safety.
//
// The two classes it returns are described on [selectStuckSQL]. Both arrive
// with their full step records, because the answer to "what is held" lives in a
// step's Output: the checkout saga writes its reservation ids there and keeps
// them readable after compensation, precisely so an operator can find them.
//
// The staleness cutoff is computed INSIDE the statement and handed back as its
// first column, so the instant that CHOSE the rows is the instant reported with
// them. Reading it separately would leave two values that can disagree, and the
// disagreement is invisible in a test because a test's caller and database
// share one host: a build that filtered on the caller's clock while reporting
// the database's passed the whole suite (measured, by mutation). Now there is
// only one expression, so there is nothing to keep in step.
//
// Timestamps are compared against the DATABASE clock, not the caller's. The
// records were written with now() on the server (see insertExecutionSQL) and
// mixing the two clocks would make the staleness cutoff drift by whatever the
// caller's host is off by.
func (r *Reader) Stuck(ctx context.Context, filter StuckFilter) (StuckPage, error) {
	if filter.StaleAfter <= 0 {
		return StuckPage{}, errors.Invalid(CodeFilterInvalid,
			"StaleAfter must be positive; a zero cutoff would list sagas that are still "+
				"running and invite releasing stock a live compensation is about to release")
	}
	if filter.Limit <= 0 {
		return StuckPage{}, errors.Invalid(CodeFilterInvalid,
			"Limit must be positive; an unbounded listing would read an incident-sized "+
				"table into memory and could not report that it had stopped early")
	}
	name, err := stuckWorkflowParam(filter.Workflow)
	if err != nil {
		return StuckPage{}, err
	}

	executions, staleBefore, err := r.probe(ctx, name, filter)
	if err != nil {
		return StuckPage{}, err
	}

	// An empty listing has no row to carry the cutoff, so the header falls back
	// to evaluating the same expression on its own. That value cannot mislead
	// anyone: with no rows there is nothing it could describe wrongly, and it is
	// never the value a selection was made with — that one always arrives with
	// the rows it selected.
	if len(executions) == 0 {
		if staleBefore, err = r.cutoff(ctx, filter.StaleAfter); err != nil {
			return StuckPage{}, err
		}
	}

	page := StuckPage{StaleBefore: staleBefore.UTC(), Limit: filter.Limit}
	if len(executions) > filter.Limit {
		executions = executions[:filter.Limit]
		page.Truncated = true
	}
	page.Executions = executions

	return page, nil
}

// cutoff evaluates the staleness instant for a listing that matched nothing.
//
// It exists ONLY for the empty page's header; the selecting cutoff is the one
// [selectStuckSQL] computes and returns with its rows. Keeping the two apart is
// the point: this value never reaches a comparison, so it cannot drift into one.
func (r *Reader) cutoff(ctx context.Context, staleAfter time.Duration) (time.Time, error) {
	pool, err := driverPool(r.pool)
	if err != nil {
		return time.Time{}, err
	}

	var at time.Time
	if err := pool.QueryRow(ctx,
		`SELECT now() - make_interval(secs => $1::double precision)`, staleAfter.Seconds()).
		Scan(&at); err != nil {
		return time.Time{}, wrapDB(err, CodeQueryFailed, "staleness cutoff could not be read")
	}

	return at, nil
}

// probe runs the query and returns AT MOST Limit+1 executions.
//
// The one extra row is the truncation probe: it is what lets "the page is full"
// be told apart from "the page is full AND there is more" without a second scan
// of the table for a number the operator only needs as a yes/no.
//
// It is a separate method so the bound can be measured. Cutting the page in Go
// makes the caller-visible result identical whether the database was asked for
// Limit+1 rows or for a million, and the difference between those is an
// unbounded read at incident scale — a limit that never reaches what it bounds
// is the exact failure class this repository keeps finding. The in-package test
// calls this directly and counts.
func (r *Reader) probe(
	ctx context.Context,
	name string,
	filter StuckFilter,
) ([]*workflow.Execution, time.Time, error) {
	pool, err := driverPool(r.pool)
	if err != nil {
		return nil, time.Time{}, err
	}

	rows, err := pool.Query(ctx, selectStuckSQL,
		name,
		string(workflow.StatusCompensationFailed),
		string(workflow.StatusRunning),
		filter.StaleAfter.Seconds(),
		heldStatusParam(),
		filter.Limit+1,
	)
	if err != nil {
		return nil, time.Time{}, wrapDB(err, CodeQueryFailed, "stuck executions could not be read")
	}
	defer rows.Close()

	executions, staleBefore, err := foldStuckRows(rows)
	if err != nil {
		return nil, time.Time{}, err
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, wrapDB(err, CodeQueryFailed, "stuck execution rows could not be read")
	}

	return executions, staleBefore, nil
}

// stuckWorkflowParam validates the optional workflow filter.
//
// An empty name means "every workflow" and is the normal case. A name that is
// only whitespace is REJECTED rather than treated as empty: it would silently
// widen the listing from one workflow to all of them, which is the opposite of
// what the operator typed. keyParam refuses a blank idempotency key for the
// same reason.
func stuckWorkflowParam(name string) (string, error) {
	if name == "" {
		return "", nil
	}

	return requireText(name, "workflow name", maxNameLen)
}

// heldStatusParam turns the engine's held-status list into a query parameter.
//
// The conversion exists because pgx sends a []string as text[] while
// []workflow.StepStatus is a named type it would have to be taught about. The
// list itself is NOT written here; it comes from the engine so that the SQL
// filter and the engine's abandonment decision cannot disagree (see
// [workflow.HeldStepStatuses]).
func heldStatusParam() []string {
	held := workflow.HeldStepStatuses()
	out := make([]string, len(held))
	for i := range held {
		out[i] = string(held[i])
	}

	return out
}

// foldStuckRows folds join rows into one execution per id, in row order.
//
// It differs from [foldRows] in one way that matters: the execution columns are
// scanned on EVERY row rather than skipped after the first. foldRows can blank
// them because it reads a single execution; here the id is what marks the
// boundary between two executions, so it has to arrive with every row. The
// repeated input/output allocations are bounded by the page limit and this runs
// once, by hand, during an incident.
func foldStuckRows(rows rowSource) ([]*workflow.Execution, time.Time, error) {
	var (
		out         []*workflow.Execution
		current     *workflow.Execution
		row         execRow
		step        stepRow
		staleBefore time.Time
		targets     = append([]any{&staleBefore}, scanTargets(&row, &step)...)
	)

	for rows.Next() {
		// The targets are shared between rows, so the step fields are cleared:
		// the driver does nil a target on a NULL column, but leaning on that
		// would leave a silent copy bug one refactor away. foldRows makes the
		// same choice for the same reason.
		step = stepRow{}
		if err := rows.Scan(targets...); err != nil {
			return nil, time.Time{}, wrapDB(err, CodeQueryFailed, "stuck execution row could not be decoded")
		}

		if current == nil || current.ID != row.id {
			current = row.execution()
			out = append(out, current)
		}
		// An execution with no steps at all arrives as a single row with NULL
		// step columns (LEFT JOIN); a NULL step_index means there is no step.
		if step.index != nil {
			current.Steps = append(current.Steps, step.record())
		}
	}

	return out, staleBefore, nil
}
