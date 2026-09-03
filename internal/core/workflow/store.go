package workflow

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"time"
)

// Status is an execution's overall state.
//
// The four values have to be read by the trace the execution left in the world:
// completed means "the work was done", failed means "the work was done and
// UNDONE", and compensation_failed means "the work was done and could not be
// undone". Without that distinction an operator cannot tell which execution
// needs a human hand.
type Status string

// The execution states.
const (
	// StatusRunning reports that the execution is still going. An execution
	// record is opened in this state and moves only into one of the terminal
	// states.
	StatusRunning Status = "running"
	// StatusCompleted reports that every step finished successfully.
	StatusCompleted Status = "completed"
	// StatusFailed reports that a step blew up and compensation completed
	// SUCCESSFULLY: no work is left half-done, the system is consistent.
	//
	// # Moving into this state RELEASES the idempotency key
	//
	// By its meaning: "it was compensated" is exactly "this attempt left no
	// trace in the world", and the key is a trace too. Were it not released —
	// and once it was not — every later call with the same key would get a 409
	// forever. In the storefront that read: a customer whose card was declined
	// COULD NEVER PAY FOR THAT CART AGAIN. And because the key is derived from
	// the cart id, the advice to "use a new key" had no counterpart on the HTTP
	// surface either.
	//
	// The record is NOT deleted, only its key drops: the failed attempt stays
	// as an audit record.
	//
	// None of the other terminal states releases the key, and none should:
	// were [StatusCompleted] to release it the same cart would be charged
	// twice, and were [StatusCompensationFailed] to release it a new attempt
	// would land on top of half-done work waiting for a human.
	StatusFailed Status = "failed"
	// StatusCompensationFailed reports that both the step and its compensation
	// blew up. The system is left inconsistent; A HUMAN IS NEEDED. A monitoring
	// rule should count this state first.
	StatusCompensationFailed Status = "compensation_failed"
)

// StepStatus is a single step's final state.
type StepStatus string

// The step states.
const (
	// StepInvoked reports that the step's Invoke finished successfully.
	StepInvoked StepStatus = "invoked"
	// StepFailed reports that the step's Invoke blew up. As a rule that step is
	// NOT COMPENSATED; a failed Invoke has no work to undo. The one exception is
	// a retry the engine ITSELF triggered: when Attempts > 1 the step is
	// compensated on a best-effort basis and its record is updated to
	// compensated or compensation_failed (see the package comment).
	StepFailed StepStatus = "failed"
	// StepCompensated reports that the step's Compensate ran successfully.
	StepCompensated StepStatus = "compensated"
	// StepCompensationFailed reports that the step's Compensate blew up.
	// That step's side effect is left HANGING in the system.
	StepCompensationFailed StepStatus = "compensation_failed"
)

// Held reports that the step's side effect is STILL STANDING in the world.
//
// invoked means "the work was done and not compensated" and
// compensation_failed means "the work was done and could not be undone"; both
// leave a hanging side effect. compensated has been undone and failed never did
// any work — neither is HANGING.
//
// The predicate is the engine's abandoned-execution decision itself (see
// [WithLease]): it is what decides whether a record whose lease expired becomes
// [StatusFailed] or [StatusCompensationFailed]. The surface that LISTS the
// records waiting for a human has to use the same predicate; written separately,
// the listing would one day quietly skip a record the engine counts as "work
// done" and the hanging reservation would stay invisible.
func (s StepStatus) Held() bool {
	return s == StepInvoked || s == StepCompensationFailed
}

// HeldStepStatuses is EVERY state for which [StepStatus.Held] is true.
//
// The list exists to carry the predicate into SQL: the database cannot call a Go
// method, so the filter goes to the query as a PARAMETER. The same solution is
// used in pgstore's updateStatusSQL — embedding a second copy of a status
// constant in the SQL text would quietly stop the rule from working the day the
// two copies diverged.
//
// That the list agrees with the predicate is checked by a test (by parsing the
// status constants out of the source); a hand-kept list would silently fall
// short the moment a new step state was added.
//
// The returned slice is NEW on every call: were a package-level variable shared,
// a caller could change the engine's decision by sorting it or writing over it.
func HeldStepStatuses() []StepStatus {
	return []StepStatus{StepInvoked, StepCompensationFailed}
}

// The name and key length limits.
//
// The limits are part of the Store CONTRACT. A durable implementation indexes
// these fields and an unbounded value turns into an unintelligible driver error
// there; keeping the limit in the engine buys two things — implementations
// behave the same on the same input (there is no workflow that passes on the
// in-memory Store and fails on Postgres) and the error reaches the caller BEFORE
// any side effect is applied. The engine applies them in Workflow.Validate and
// WithIdempotencyKey; Store implementations MUST ACCEPT at least these lengths.
const (
	// MaxNameLen is the upper bound in bytes on workflow and step names.
	MaxNameLen = 128
	// MaxIdempotencyKeyLen is the upper bound in bytes on the idempotency key.
	MaxIdempotencyKeyLen = 256
)

// StepRecord is a single step's durable trace.
type StepRecord struct {
	// Name is the name the step reports through Step.Name().
	Name string
	// Index is the step's order within the workflow and is the record's
	// IDENTITY: Store.AppendStep updates the record with the same Index. When a
	// step is written first as invoked and then as compensated, the same row is
	// updated.
	Index int
	// Status is the step's final state.
	Status StepStatus
	// Output is the JSON form of the value Invoke returned.
	//
	// When Status is invoked and this field is empty while Failure is set, the
	// step SUCCEEDED but its output could not be turned into JSON; see
	// Executor.Run's persistence policy.
	//
	// The compensation record does NOT erase this field: Invoke's output stays
	// readable in the compensated and compensation_failed states too — the only
	// data an operator doing manual repair needs (which reservation, which
	// payment) is here.
	Output json.RawMessage
	// Failure is the error message; it is empty when the step and its
	// compensation both succeeded.
	//
	// On a step compensated on a best-effort basis after Invoke blew up,
	// Invoke's error is PRESERVED; if the compensation blows up too the two
	// messages are joined with ";".
	Failure string
	// Attempts is the number of INVOKE attempts for the step (including the
	// first, at least 1). The compensation record preserves this count;
	// compensation attempts go to the log, not to the record.
	Attempts int
	// StartedAt is the instant Invoke's first attempt began (UTC); the
	// compensation record preserves it.
	StartedAt time.Time
	// EndedAt is the instant the last work written to the record ended (UTC):
	// the compensation's end if the step was compensated, otherwise the end of
	// the last Invoke attempt.
	EndedAt time.Time
}

// Execution is the durable state of one workflow run.
type Execution struct {
	// ID is the execution's unique id (prefixed with "wfx_", time-ordered).
	ID string
	// Workflow is the name of the workflow that ran.
	Workflow string
	// IdempotencyKey is the repeat-protection key the caller supplied; it is
	// empty when none was given. The Store enforces uniqueness of the
	// (Workflow, IdempotencyKey) pair only while the key is NOT empty.
	IdempotencyKey string
	// Status is the execution's overall state.
	Status Status
	// Input is the JSON form of the workflow's input.
	Input json.RawMessage
	// Output is the JSON form of the last step's output; it is meaningful only
	// in the completed state.
	Output json.RawMessage
	// Failure is the error message of the terminal state; it is empty on a
	// successful run.
	Failure string
	// Steps are the step records, in execution order.
	Steps []StepRecord
	// CreatedAt is the instant the record was opened (UTC).
	CreatedAt time.Time
	// UpdatedAt is the instant of the last write (UTC).
	UpdatedAt time.Time
}

// Store persists execution state.
//
// The engine defines this interface as the CONSUMER (the ADR 0001 pattern): the
// Postgres implementation is in a separate package and this package does not
// import it. For the in-process implementation see NewMemoryStore.
//
// Implementations have to be safe for concurrent calls: several executions can
// run at once, and although one execution's steps are written from a single
// goroutine, Get/FindByIdempotencyKey can be read from others.
//
// For the name and key fields implementations must accept at least MaxNameLen
// and MaxIdempotencyKeyLen; the engine already rejects anything above those
// limits. NO MEANING MAY BE ATTACHED to leading or trailing whitespace in names:
// an implementation may normalize these fields (by trimming, say) and two
// implementations may store the same value differently.
type Store interface {
	// Create opens a new execution record. If the same
	// (Workflow, IdempotencyKey) pair already exists it returns errors.Conflict.
	Create(ctx context.Context, exec *Execution) error
	// FindByIdempotencyKey returns the execution the key belongs to, or
	// errors.NotFound.
	FindByIdempotencyKey(ctx context.Context, workflow, key string) (*Execution, error)
	// AppendStep inserts a step record or updates the one with the same Index.
	AppendStep(ctx context.Context, executionID string, rec StepRecord) error
	// UpdateStatus writes the execution's final status.
	//
	// When [StatusFailed] is written the implementation must also release the
	// execution's idempotency KEY (without deleting the record). The reasoning
	// is in the [StatusFailed] godoc, and this is part of the same write rather
	// than a separate method: a process dying between two separate writes would
	// leave the key held forever — that is, it would bring back the very failure
	// that was fixed, as a rare race.
	UpdateStatus(ctx context.Context, executionID string, status Status, output json.RawMessage, failure string) error
	// Get reads the execution together with its steps, or errors.NotFound.
	Get(ctx context.Context, executionID string) (*Execution, error)
}

// executionIDPrefix is the prefix of execution ids (plan Section 8).
const executionIDPrefix = "wfx_"

// idEncoding is the Crockford Base32 alphabet, chosen so the id string is both
// sortable and readable by a human (it has no I/L/O/U).
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// newExecutionID generates a time-ordered, unique execution id.
//
// Its shape is a ULID's: a 48-bit millisecond timestamp plus 80 bits of
// cryptographic randomness, encoded to 26 Crockford Base32 characters. The same
// approach is used for event ids in eventbus.
func newExecutionID(t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// A pre-1970 timestamp is meaningless for an execution; it is clamped
		// to the floor so it cannot break the ordering.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	var buf [16]byte
	// UnixMilli fits in 48 bits; the first two bytes are always zero and are
	// dropped.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read does not return an error; should it ever start to,
		// the id falls back to nanosecond resolution — uniqueness gets weaker,
		// but starting an execution does not become impossible.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}

	return executionIDPrefix + idEncoding.EncodeToString(buf[:])
}
