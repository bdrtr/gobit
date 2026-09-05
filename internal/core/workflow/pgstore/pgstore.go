// Package pgstore persists workflow execution state in PostgreSQL.
//
// The package implements the [workflow.Store] interface. The CONSUMING side
// (the engine) defines that interface and this package only satisfies the
// signature — ADR 0001's consumer-side interface pattern. The engine does not
// import this package; the concrete store is resolved from the container.
//
// The schema is two tables: workflow_executions (the execution itself) and
// workflow_execution_steps (the step records). The migrations are embedded in
// the package; the core applies them with db.Migrate (see [Migrations],
// [MigrationOwner]).
//
// Three rules hold across the package:
//
//   - An execution's input and output are BUSINESS DATA; they appear in no log
//     record (plan Section 8, "sensitive data is not logged"). The logs carry
//     only the id, the workflow name and the status.
//   - Every value reaches a query as a PARAMETER; no SQL string is concatenated
//     with runtime data.
//   - Every error leaving the package is typed with core/errors; the raw driver
//     error stays in the chain and is reachable with errors.Is/As.
package pgstore

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// Error codes; the caller can branch on them with errors.CodeOf.
const (
	// CodeInvalid reports that the input is in a state the store cannot write.
	CodeInvalid = "workflow_store_invalid"
	// CodeNotFound reports that the requested execution does not exist.
	CodeNotFound = "workflow_execution_not_found"
	// CodeDuplicateKey reports that the same (workflow, idempotency_key) pair is
	// already in use. The engine recognizes an idempotent repeat by this code,
	// and it is the ONLY failure the store returns with the Conflict class (see
	// createError).
	CodeDuplicateKey = "workflow_execution_duplicate_key"
	// CodeDuplicateID reports that a record was opened a second time with the
	// same id. Its class is Invalid: giving the same id twice is the caller's
	// input error, not a repeat request.
	CodeDuplicateID = "workflow_execution_duplicate_id"
	// CodeConflict reports an unrecognized uniqueness violation; the schema has
	// drifted from the assumption in the code. Its class is Internal — saying
	// Conflict without knowing what happened would send the engine down the
	// replay path.
	CodeConflict = "workflow_store_conflict"
	// CodeQueryFailed reports that the query failed at the driver level.
	CodeQueryFailed = "workflow_store_query_failed"
	// CodeUnavailable reports that the database pool was never built.
	CodeUnavailable = "workflow_store_unavailable"
	// CodeCanceled reports that the context was canceled before the work ended.
	CodeCanceled = "workflow_store_canceled"
)

// The keys used in log fields and error details.
const (
	keyExecutionID = "execution_id"
	keyWorkflow    = "workflow"
	keyStatus      = "status"
	keyStepIndex   = "step_index"
)

// store is the PostgreSQL implementation of the [workflow.Store] interface.
// It is safe for concurrent use; its state is only the pool and the logger.
type store struct {
	pool *db.Pool
	log  *slog.Logger
}

// That store satisfies the contract is verified at compile time.
var (
	_ workflow.Store         = (*store)(nil)
	_ workflow.ClaimingStore = (*store)(nil)
)

// New returns an execution store running on the given pool.
//
// A nil log means nothing is logged. With a nil pool New still returns a store;
// every method then fails with a typed error of class KindUnavailable — the
// constructor not returning an error is what the contract
// (func New(...) workflow.Store) requires.
func New(pool *db.Pool, log *slog.Logger) workflow.Store {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &store{pool: pool, log: log}
}

// Create opens a new execution record.
//
// It fills the fields that were left empty and writes them back into the
// caller's struct: an empty ID gets a new id with the "wfx_" prefix (see
// newExecutionID), an empty Status becomes StatusRunning, and
// CreatedAt/UpdatedAt come from the database clock. The timestamps the caller
// supplied are ignored: with several replicas writing, the only correct clock is
// the database's.
//
// exec.Steps is IGNORED; step records are added only through AppendStep.
//
// If the same (Workflow, IdempotencyKey) pair already exists it returns
// errors.Conflict (code: CodeDuplicateKey). That decision is taken by catching
// the violation of the partial unique index rather than with a SELECT, which is
// why only one of two processes calling at the same instant is guaranteed to
// succeed. The Conflict class is reserved for that case ALONE: giving the same
// ID a second time returns errors.Invalid (code: CodeDuplicateID), because that
// is an input error rather than a repeat request (see createError).
//
// An empty IdempotencyKey counts as "no key"; a key made of whitespace only
// returns errors.Invalid — were it silently taken as keyless, the repeat
// protection the caller asked for would vanish without a warning.
//
// exec.Failure is MADE writable (NUL bytes and invalid UTF-8 sequences are
// dropped, see safeText) and the cleaned form is written back into the caller's
// struct.
func (s *store) Create(ctx context.Context, exec *workflow.Execution) error {
	if exec == nil {
		return errors.Invalid(CodeInvalid, "the execution record cannot be nil")
	}

	name, err := requireText(exec.Workflow, "the workflow name", maxNameLen)
	if err != nil {
		return err
	}

	status := exec.Status
	if strings.TrimSpace(string(status)) == "" {
		status = workflow.StatusRunning
	}
	statusText, err := requireText(string(status), "the status", maxNameLen)
	if err != nil {
		return err
	}

	id := strings.TrimSpace(exec.ID)
	if id == "" {
		id = newExecutionID(time.Now())
	} else {
		id, err = requireText(id, "the execution id", maxIDLen)
		if err != nil {
			return err
		}
	}

	key, err := keyParam(exec.IdempotencyKey)
	if err != nil {
		return err
	}

	input, err := jsonParam(exec.Input, "input")
	if err != nil {
		return err
	}
	output, err := jsonParam(exec.Output, "output")
	if err != nil {
		return err
	}

	pool, err := s.rawPool()
	if err != nil {
		return err
	}

	failure := safeText(exec.Failure)

	var createdAt, updatedAt time.Time
	err = pool.QueryRow(ctx, insertExecutionSQL,
		id, name, key, statusText, input, output, failure,
	).Scan(&createdAt, &updatedAt)
	if err != nil {
		return createError(err, id, name, exec.IdempotencyKey)
	}

	exec.ID = id
	exec.Workflow = name
	exec.Status = workflow.Status(statusText)
	exec.Failure = failure
	exec.CreatedAt = createdAt.UTC()
	exec.UpdatedAt = updatedAt.UTC()

	s.log.DebugContext(ctx, "workflow execution opened",
		slog.String(keyExecutionID, exec.ID),
		slog.String(keyWorkflow, exec.Workflow),
		slog.String(keyStatus, statusText),
	)

	return nil
}

// FindByIdempotencyKey returns the execution the key belongs to, or
// errors.NotFound.
//
// The record it returns carries its steps too (in the same shape as Get, in
// Index order): on an idempotent repeat the engine has to be able to see not
// only the result but also where the run had got to.
//
// The key must come from the set Create accepts: an empty key cannot be looked
// up — it is stored as NULL and selects no single record — and a key the write
// path rejects (whitespace only, over the length limit) returns errors.Invalid
// here as well. Were the two paths' accepted sets to diverge, a key that could
// be written could not be read back, or one that could be read could never be
// written.
func (s *store) FindByIdempotencyKey(ctx context.Context, wf, key string) (*workflow.Execution, error) {
	name, err := requireText(wf, "the workflow name", maxNameLen)
	if err != nil {
		return nil, err
	}
	wanted, err := keyParam(key)
	if err != nil {
		return nil, err
	}
	if wanted == nil {
		return nil, errors.Invalid(CodeInvalid, "the idempotency key cannot be empty")
	}

	exec, err := s.queryExecution(ctx, selectByKeySQL, name, *wanted)
	if err != nil {
		return nil, err
	}
	if exec == nil {
		return nil, errors.NotFound(CodeNotFound,
			"the %s workflow has no execution with the idempotency key %q", name, key).
			WithDetails(map[string]any{keyWorkflow: name})
	}

	return exec, nil
}

// AppendStep inserts a step record or updates the one with the same Index.
//
// The update path is for retries: when the same step is tried again no second
// row is opened, the existing record (including Attempts) is overwritten. The
// call also refreshes the execution's UpdatedAt.
//
// rec.Failure is MADE writable (see safeText); losing the step's trace entirely
// over a piece of diagnostic text would be worse than the error itself.
//
// If the execution does not exist it returns errors.NotFound.
func (s *store) AppendStep(ctx context.Context, executionID string, rec workflow.StepRecord) error {
	id, err := requireText(executionID, "the execution id", maxIDLen)
	if err != nil {
		return err
	}
	stepName, err := requireText(rec.Name, "the step name", maxNameLen)
	if err != nil {
		return err
	}
	stepStatus, err := requireText(string(rec.Status), "the step status", maxNameLen)
	if err != nil {
		return err
	}
	index, err := requireCount(rec.Index, "the step order (Index)")
	if err != nil {
		return err
	}
	attempts, err := requireCount(rec.Attempts, "the attempt count (Attempts)")
	if err != nil {
		return err
	}
	output, err := jsonParam(rec.Output, "the step output")
	if err != nil {
		return err
	}

	pool, err := s.rawPool()
	if err != nil {
		return err
	}

	tag, err := pool.Exec(ctx, upsertStepSQL,
		id, index, stepName, stepStatus, output, safeText(rec.Failure), attempts,
		timeParam(rec.StartedAt), timeParam(rec.EndedAt),
	)
	if err != nil {
		return wrapDB(err, CodeQueryFailed,
			"step %d of execution %q could not be written", rec.Index, id)
	}
	if tag.RowsAffected() == 0 {
		// The foreign key does not normally allow this; the check is here so
		// that a step written with no owner does not pass in silence should the
		// constraint ever be dropped.
		return errors.NotFound(CodeNotFound,
			"there is no execution with the id %q; the step could not be written", id).
			WithDetails(map[string]any{keyExecutionID: id, keyStepIndex: rec.Index})
	}

	s.log.DebugContext(ctx, "workflow step written",
		slog.String(keyExecutionID, id),
		slog.Int(keyStepIndex, rec.Index),
		slog.String(keyStatus, stepStatus),
	)

	return nil
}

// UpdateStatus writes the execution's final status.
//
// A nil output sets the column to NULL; an empty failure clears the failure
// description. failure is MADE writable (see safeText): writing the terminal
// state comes before keeping the description intact — a terminal state that
// could not be written would leave the record "running" forever.
//
// If the execution does not exist it returns errors.NotFound.
func (s *store) UpdateStatus(
	ctx context.Context,
	executionID string,
	status workflow.Status,
	output json.RawMessage,
	failure string,
) error {
	id, err := requireText(executionID, "the execution id", maxIDLen)
	if err != nil {
		return err
	}
	statusText, err := requireText(string(status), "the status", maxNameLen)
	if err != nil {
		return err
	}
	outputParam, err := jsonParam(output, "output")
	if err != nil {
		return err
	}

	pool, err := s.rawPool()
	if err != nil {
		return err
	}

	// If compensation completed in full the execution left NO TRACE in the
	// world; the key is a trace too, and it is released. The reasoning is in
	// the [workflow.StatusFailed] godoc.
	tag, err := pool.Exec(ctx, updateStatusSQL, id, statusText, outputParam, safeText(failure),
		status == workflow.StatusFailed)
	if err != nil {
		return wrapDB(err, CodeQueryFailed, "the status of execution %q could not be written", id)
	}
	if tag.RowsAffected() == 0 {
		return errors.NotFound(CodeNotFound, "there is no execution with the id %q", id).
			WithDetails(map[string]any{keyExecutionID: id})
	}

	s.log.DebugContext(ctx, "workflow execution status updated",
		slog.String(keyExecutionID, id),
		slog.String(keyStatus, statusText),
	)

	return nil
}

// ClaimAbandoned claims the execution for recovery and reports whether the claim
// was won.
//
// The claim is a single conditional UPDATE ([claimAbandonedSQL]): it succeeds
// only while the record is still running AND its updated_at is still the value
// the caller saw. Winning it stamps updated_at with the current instant, which
// both excludes the other processes and renews the lease.
//
// A record that is no longer there is a LOST claim, not an error: the caller's
// question is "may I recover this", and the answer for a record that vanished is
// no. Every other outcome an operator needs to see is in the returned error.
func (s *store) ClaimAbandoned(ctx context.Context, executionID string, seen time.Time) (bool, error) {
	id, err := requireText(executionID, "the execution id", maxIDLen)
	if err != nil {
		return false, err
	}

	pool, err := s.rawPool()
	if err != nil {
		return false, err
	}

	tag, err := pool.Exec(ctx, claimAbandonedSQL, id, string(workflow.StatusRunning), seen.UTC())
	if err != nil {
		return false, wrapDB(err, CodeQueryFailed, "execution %q could not be claimed for recovery", id)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}

	s.log.WarnContext(ctx, "workflow execution claimed for recovery",
		slog.String(keyExecutionID, id),
	)

	return true, nil
}

// Get reads the execution together with its steps, or errors.NotFound.
// The steps come back in ascending Index order.
func (s *store) Get(ctx context.Context, executionID string) (*workflow.Execution, error) {
	id, err := requireText(executionID, "the execution id", maxIDLen)
	if err != nil {
		return nil, err
	}

	exec, err := s.queryExecution(ctx, selectByIDSQL, id)
	if err != nil {
		return nil, err
	}
	if exec == nil {
		return nil, errors.NotFound(CodeNotFound, "there is no execution with the id %q", id).
			WithDetails(map[string]any{keyExecutionID: id})
	}

	return exec, nil
}

// queryExecution runs a query that selects a single execution and folds the
// rows into an execution plus its list of steps.
//
// It returns (nil, nil) when no record is found: the message for "not there"
// differs by call path, so the caller produces the error.
func (s *store) queryExecution(ctx context.Context, sql string, args ...any) (*workflow.Execution, error) {
	pool, err := s.rawPool()
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, wrapDB(err, CodeQueryFailed, "the execution could not be read")
	}
	defer rows.Close()

	exec, err := foldRows(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDB(err, CodeQueryFailed, "the execution rows could not be read")
	}

	return exec, nil
}

// rowSource is the smallest read surface foldRows needs; pgx.Rows satisfies it.
// The narrow interface is what lets the folding logic be tested without a
// database (ADR 0001: the CONSUMING side defines the interface).
type rowSource interface {
	Next() bool
	Scan(dest ...any) error
}

// foldRows folds the join rows into a single execution.
//
// The execution columns are scanned ONLY ON THE FIRST ROW; the reasoning is in
// [skipExecColumns]. With no rows at all it returns (nil, nil).
func foldRows(rows rowSource) (*workflow.Execution, error) {
	var (
		exec    *workflow.Execution
		row     execRow
		step    stepRow
		targets = scanTargets(&row, &step)
	)
	for rows.Next() {
		// The targets are shared between rows, so the step fields are cleared:
		// the driver does nil a target on a NULL column, but leaning on that
		// assumption would leave the door open to a silent copy bug.
		step = stepRow{}
		if err := rows.Scan(targets...); err != nil {
			return nil, wrapDB(err, CodeQueryFailed, "the execution row could not be decoded")
		}

		if exec == nil {
			exec = row.execution()
			skipExecColumns(targets)
		}
		// Under the LEFT JOIN an execution with no steps arrives as a single
		// row with NULL step columns; a NULL step_index means there is no step.
		if step.index != nil {
			exec.Steps = append(exec.Steps, step.record())
		}
	}

	return exec, nil
}

// execColumnCount is the number of execution columns in a join row.
const execColumnCount = 9

// scanTargets builds the scan targets for one join row.
//
// The order is the column order in selectExecutionSQL; the two have to change
// together.
func scanTargets(row *execRow, step *stepRow) []any {
	return []any{
		&row.id, &row.name, &row.key, &row.status, &row.input, &row.output, &row.failure,
		&row.createdAt, &row.updatedAt,
		&step.index, &step.name, &step.status, &step.output, &step.failure, &step.attempts,
		&step.startedAt, &step.endedAt,
	}
}

// skipExecColumns empties the scan targets of the execution columns.
//
// The LEFT JOIN carries the execution row AGAIN for every step; everything
// after the first row is a copy of the same data. pgx skips a nil target
// (Rows.Scan: "nil will skip the value entirely"), so a 100 KB input is not
// allocated twenty times over and thrown away at once in a twenty-step
// execution. Measured: Get on a record with a 256 KB input and eight steps
// allocates 0.28 MB instead of 2.17 MB.
//
// The columns still come over the wire: that is the price of reading an
// execution and its steps in ONE statement (one snapshot) — see
// selectExecutionSQL.
func skipExecColumns(targets []any) {
	for i := range execColumnCount {
		targets[i] = nil
	}
}

// rawPool returns the raw pgx pool and produces a typed error when the pool was
// never built.
//
// The body is in the package-level [driverPool]: the listing reader has to make
// the same check, and were the two copies to diverge one would give a typed
// error on a nil pool while the other went down with a panic.
func (s *store) rawPool() (*pgxpool.Pool, error) {
	return driverPool(s.pool)
}

// driverPool returns the pgx pool underneath the wrapper.
//
// The store and the listing reader share it so that "the pool was never built"
// produces the SAME message and the same error class whichever surface it is
// reached from. Were the two copies to diverge, one could give a typed error
// while the other panicked on a nil pool.
func driverPool(pool *db.Pool) (*pgxpool.Pool, error) {
	// db.Pool.Pool() is safe against a nil receiver; a nil pool returns nil.
	raw := pool.Pool()
	if raw == nil {
		return nil, errors.Unavailable(CodeUnavailable,
			"the database pool for the workflow store was never built")
	}

	return raw, nil
}

// execRow is the raw read shape of a workflow_executions row.
type execRow struct {
	id        string
	name      string
	key       *string
	status    string
	input     []byte
	output    []byte
	failure   string
	createdAt time.Time
	updatedAt time.Time
}

// execution turns the raw row into the execution type of the contract.
func (r execRow) execution() *workflow.Execution {
	return &workflow.Execution{
		ID:             r.id,
		Workflow:       r.name,
		IdempotencyKey: keyValue(r.key),
		Status:         workflow.Status(r.status),
		Input:          jsonValue(r.input),
		Output:         jsonValue(r.output),
		Failure:        r.failure,
		CreatedAt:      r.createdAt.UTC(),
		UpdatedAt:      r.updatedAt.UTC(),
	}
}

// stepRow is the raw read shape of a workflow_execution_steps row.
// The fields are pointers so they can carry the NULLs the LEFT JOIN produces.
type stepRow struct {
	index     *int32
	name      *string
	status    *string
	output    []byte
	failure   *string
	attempts  *int32
	startedAt *time.Time
	endedAt   *time.Time
}

// record turns the raw row into the step record of the contract.
// It is called only when index is non-nil.
func (r stepRow) record() workflow.StepRecord {
	return workflow.StepRecord{
		Name:      textValue(r.name),
		Index:     int(*r.index),
		Status:    workflow.StepStatus(textValue(r.status)),
		Output:    jsonValue(r.output),
		Failure:   textValue(r.failure),
		Attempts:  countValue(r.attempts),
		StartedAt: timeValue(r.startedAt),
		EndedAt:   timeValue(r.endedAt),
	}
}

// textValue turns NULL text into the empty string.
func textValue(v *string) string {
	if v == nil {
		return ""
	}

	return *v
}

// countValue turns a NULL counter into zero.
func countValue(v *int32) int {
	if v == nil {
		return 0
	}

	return int(*v)
}
