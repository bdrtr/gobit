package pgstore

// PostgreSQL SQLSTATE codes. (The codes in use are written out rather than
// taking a dependency on github.com/jackc/pgerrcode; the link package does the
// same.)
//
// The last three arise from the CALLER'S DATA: a value that cannot be converted
// to the column's type (a NUL byte in text, a NUL escape in JSON, broken
// UTF-8). That is why they map to KindInvalid; see wrapDB. That these really
// are the codes is verified against a live server in the integration test.
const (
	// uniqueViolation is a uniqueness violation (an id or idempotency clash).
	uniqueViolation = "23505"
	// foreignKeyViolation reports that the execution the step attaches to does
	// not exist.
	foreignKeyViolation = "23503"
	// checkViolation is a violation of a schema-level CHECK constraint.
	checkViolation = "23514"
	// notInRepertoire reports that the value has no representation in the
	// server encoding (a NUL byte in text, say).
	notInRepertoire = "22021"
	// untranslatableCharacter reports a Unicode escape JSONB does not support
	// (a NUL escape cannot be turned into text).
	untranslatableCharacter = "22P05"
	// invalidTextRepresentation reports that the text could not be parsed into
	// the target type (JSON carrying an unpaired surrogate, say).
	invalidTextRepresentation = "22P02"
)

// The constraint names the error mapping recognizes. Their counterparts in the
// schema are in migrations/000001_workflow_init.up.sql; that the names really
// are these is verified against the catalog (pg_class) in the integration test
// — otherwise the mapping would quietly fall through to the general branch.
const (
	// executionsPKConstraint is the primary key on the id column.
	executionsPKConstraint = "workflow_executions_pkey"
	// idempotencyIndex is the partial unique index on
	// (workflow, idempotency_key).
	idempotencyIndex = "workflow_executions_idempotency_key_uniq"
)

// insertExecutionSQL opens a new execution record.
//
// The DATABASE clock produces the timestamps: with several replicas writing to
// the same table, drift between the application clocks would break the order of
// the records. RETURNING hands back the values actually written, and the
// caller's struct is filled from those.
const insertExecutionSQL = `
INSERT INTO workflow_executions (
	id, workflow, idempotency_key, status, input, output, failure, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now())
RETURNING created_at, updated_at`

// upsertStepSQL inserts the step record or updates the one with the same index.
//
// It is a single statement: the insert happens inside a data-modifying CTE and
// the outer UPDATE refreshes the execution's updated_at. Had two separate
// statements been chosen, an error landing between them could leave the step
// written and the execution on a stale updated_at.
//
// The ON CONFLICT target (execution_id, step_index) is the primary key: when a
// retry rewrites the same step no new row is OPENED, and every field including
// attempts is updated.
const upsertStepSQL = `
WITH written AS (
	INSERT INTO workflow_execution_steps (
		execution_id, step_index, name, status, output, failure, attempts, started_at, ended_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	ON CONFLICT (execution_id, step_index) DO UPDATE SET
		name       = EXCLUDED.name,
		status     = EXCLUDED.status,
		output     = EXCLUDED.output,
		failure    = EXCLUDED.failure,
		attempts   = EXCLUDED.attempts,
		started_at = EXCLUDED.started_at,
		ended_at   = EXCLUDED.ended_at
	RETURNING execution_id
)
UPDATE workflow_executions e
SET updated_at = now()
FROM written
WHERE e.id = written.execution_id`

// updateStatusSQL writes the execution's final status.
// $5 is whether the idempotency key is to be RELEASED.
//
// The decision is taken on the Go side and arrives here as a boolean; embedding
// the 'failed' string in the SQL would produce a second copy of the status
// constant, and the rule would quietly stop working the day the two copies
// diverged.
//
// The key is set to NULL, the ROW IS NOT DELETED: a failed attempt has to stay
// as an audit record. Because the partial unique index covers only NON-NULL
// keys, a row set to NULL clears the way for the next attempt.
const updateStatusSQL = `
UPDATE workflow_executions
SET status = $2, output = $3, failure = $4, updated_at = now(),
    idempotency_key = CASE WHEN $5 THEN NULL ELSE idempotency_key END
WHERE id = $1`

// claimAbandonedSQL claims an abandoned execution for recovery.
//
// The whole decision is ONE statement, and that is the point: "read, decide,
// write" over three round trips is exactly the race this closes. A second
// process reaching the same row waits for the row lock, then re-evaluates the
// WHERE with the committed value and matches nothing.
//
// The condition names both the status and the instant the caller's judgement
// was based on ($3): the status alone would let a process that has been
// recovering for an hour be claimed a second time, since the record stays
// running the whole time.
//
// The status arrives as a PARAMETER and is not embedded in the text — the same
// reasoning as [updateStatusSQL]: a second copy of the constant would quietly
// stop matching the day the first one changed.
const claimAbandonedSQL = `
UPDATE workflow_executions
SET updated_at = now()
WHERE id = $1 AND status = $2 AND updated_at = $3`

// selectExecutionSQL reads the execution together with its steps in a SINGLE
// statement.
//
// The LEFT JOIN is deliberate: an execution with no steps also comes back as
// one row (its step columns are NULL). Had two separate queries been chosen, a
// write landing between them could combine an execution from one instant with
// steps from another; one statement guarantees a consistent picture.
//
// ORDER BY s.step_index satisfies the contract's "steps come back in Index
// order" on the database side.
const selectExecutionSQL = `
SELECT
	e.id, e.workflow, e.idempotency_key, e.status, e.input, e.output, e.failure,
	e.created_at, e.updated_at,
	s.step_index, s.name, s.status, s.output, s.failure, s.attempts,
	s.started_at, s.ended_at
FROM workflow_executions e
LEFT JOIN workflow_execution_steps s ON s.execution_id = e.id
`

// selectByIDSQL reads the execution by its id.
const selectByIDSQL = selectExecutionSQL + `WHERE e.id = $1
ORDER BY s.step_index`

// selectByKeySQL reads the execution by the (workflow, idempotency_key) pair.
// Thanks to the partial unique index the pair selects at most one row.
const selectByKeySQL = selectExecutionSQL + `WHERE e.workflow = $1 AND e.idempotency_key = $2
ORDER BY s.step_index`
