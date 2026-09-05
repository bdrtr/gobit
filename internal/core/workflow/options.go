package workflow

import (
	"context"
	"math"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// The default time budgets.
const (
	// DefaultCompensationTimeout is the default time given to a SINGLE
	// compensation call; the budget is per step (see WithCompensationTimeout).
	DefaultCompensationTimeout = 30 * time.Second
	// DefaultStoreTimeout is the default time given to a single Store call.
	DefaultStoreTimeout = 5 * time.Second
)

// RetryPolicy defines how many times and at what interval a step is retried.
//
// The zero value is NOT valid; WithRetry/WithCompensationRetry validate the
// policy and pull the missing fields to sensible defaults.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts (including the first). It has
	// to be at least 1; 1 means "no retries".
	MaxAttempts int
	// Backoff is the wait before the first retry. With 0 there is no wait.
	Backoff time.Duration
	// Multiplier is what the wait is multiplied by on each attempt. With 0 or 1
	// the wait is constant; 2 gives binary exponential backoff.
	Multiplier float64
	// MaxBackoff is the upper bound on the wait. With 0 there is no bound.
	MaxBackoff time.Duration
	// Retryable reports whether an error is worth retrying.
	// With nil, DefaultRetryable is used.
	//
	// The predicate does NOT decide ON ITS OWN: panics and context errors are
	// ruled out without ever asking it (see allow). Otherwise a predicate
	// saying "retry everything" would reapply the partial side effect of a
	// panicking Invoke on every attempt and spin uselessly on a dead context.
	Retryable func(error) bool
}

// NoRetry is the policy that does not retry, and it is the engine's DEFAULT.
//
// The default being "do not retry" is deliberate: the engine cannot know
// whether a step's Invoke is idempotent. Retrying a step like "charge the card"
// on its own would apply the side effect TWICE when the error happened on the
// response path (the request went, the answer was lost). Retrying is therefore
// the explicit decision of a caller who knows the step is idempotent: it is
// asked for with WithRetry.
func NoRetry() RetryPolicy {
	return RetryPolicy{MaxAttempts: 1}
}

// DefaultRetryable reports whether an error is worth retrying.
//
// The classification looks at the error's CAUSE: retrying an error that will
// give the same result for the same input only produces latency.
//
//   - KindInvalid, KindConflict, KindNotFound, KindUnauthorized, KindForbidden
//     → NOT RETRIED. It is an input, state or permission error; as long as the
//     step does not change, neither does the result.
//   - KindUnavailable → RETRIED. By definition it is transient.
//   - KindInternal → RETRIED. Unclassified errors (including untyped ones) fall
//     into this class and some of them — a network or database outage — are
//     transient; the price of optimism is a few attempts.
//   - A panic → NOT RETRIED. A panic is a programming error; repeating it
//     produces the same crash and only delays the error message.
//   - context.Canceled / DeadlineExceeded → NOT RETRIED. Retrying with a dead
//     context is starting work that has no budget.
//   - ErrUncompensated → NOT RETRIED. The step left a side effect behind that
//     could not be undone; a repeat would put a second one ON TOP of that
//     hanging work.
func DefaultRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrPanic) || errors.Is(err, ErrUncompensated) {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	switch errors.KindOf(err) {
	case errors.KindUnavailable, errors.KindInternal:
		return true
	default:
		return false
	}
}

// allow reports whether the error is retryable under the policy.
//
// Panics, context errors and "uncompensated side effect" errors are ruled out
// WITHOUT ASKING the custom predicate: those exclusions are the engine's
// guarantee (see the package comment), not the policy's preference. The custom
// predicate is consulted only for the errors that remain.
func (p RetryPolicy) allow(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrPanic) || errors.Is(err, ErrUncompensated) {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	if p.Retryable != nil {
		return p.Retryable(err)
	}

	return DefaultRetryable(err)
}

// backoffFor computes the wait after the given attempt (attempt starts at 1).
func (p RetryPolicy) backoffFor(attempt int) time.Duration {
	if p.Backoff <= 0 {
		return 0
	}

	d := float64(p.Backoff)
	if p.Multiplier > 1 && attempt > 1 {
		d *= math.Pow(p.Multiplier, float64(attempt-1))
	}
	// Overflow guard: if exponential growth passes the int64 limit it is pulled
	// to the ceiling.
	if d > float64(math.MaxInt64) {
		d = float64(math.MaxInt64)
	}

	out := time.Duration(d)
	if p.MaxBackoff > 0 && out > p.MaxBackoff {
		out = p.MaxBackoff
	}

	return out
}

// normalize validates the policy and pulls the missing fields to their defaults.
func (p RetryPolicy) normalize(what string) (RetryPolicy, error) {
	if p.MaxAttempts < 1 {
		return p, errors.Invalid(CodeInvalidOption,
			"%s: MaxAttempts has to be at least 1, %d was given", what, p.MaxAttempts)
	}
	if p.Backoff < 0 {
		return p, errors.Invalid(CodeInvalidOption,
			"%s: Backoff cannot be negative, %s was given", what, p.Backoff)
	}
	if p.MaxBackoff < 0 {
		return p, errors.Invalid(CodeInvalidOption,
			"%s: MaxBackoff cannot be negative, %s was given", what, p.MaxBackoff)
	}
	if p.Multiplier < 0 || math.IsNaN(p.Multiplier) {
		return p, errors.Invalid(CodeInvalidOption,
			"%s: Multiplier cannot be negative or NaN", what)
	}
	if p.Multiplier < 1 {
		// 0 (the zero value) means a constant wait.
		p.Multiplier = 1
	}

	return p, nil
}

// runOptions are the resolved settings of a single Run call.
type runOptions struct {
	idempotencyKey      string
	retry               RetryPolicy
	compensationRetry   RetryPolicy
	compensationTimeout time.Duration
	storeTimeout        time.Duration
	lease               time.Duration
	// compensationRetrySet holds whether the user gave a compensation policy
	// SEPARATELY; if they did not, compensation inherits the step policy.
	compensationRetrySet bool
}

// RunOption changes the behavior of an Executor.Run call.
type RunOption func(*runOptions) error

// WithIdempotencyKey binds the execution to a repeat-protection key.
//
// A second call with the same workflow name and the same key does NOT RUN the
// steps again; it behaves according to the first execution's outcome (see
// Executor.Run). The key cannot be empty and cannot exceed
// MaxIdempotencyKeyLen bytes: the limit is part of the Store contract, and
// applying it here means a key that would blow up in a durable Store's index is
// rejected before any work is done.
func WithIdempotencyKey(key string) RunOption {
	return func(o *runOptions) error {
		if key == "" {
			return errors.Invalid(CodeInvalidOption, "the idempotency key cannot be empty")
		}
		if len(key) > MaxIdempotencyKeyLen {
			return errors.Invalid(CodeInvalidOption,
				"the idempotency key can be at most %d bytes, %d bytes were given",
				MaxIdempotencyKeyLen, len(key))
		}
		o.idempotencyKey = key

		return nil
	}
}

// WithRetry sets the steps' retry policy.
//
// Unless WithCompensationRetry is given separately, compensation inherits this
// policy too.
func WithRetry(p RetryPolicy) RunOption {
	return func(o *runOptions) error {
		np, err := p.normalize("WithRetry")
		if err != nil {
			return err
		}
		o.retry = np

		return nil
	}
}

// WithCompensationRetry sets the compensation's retry policy separately.
//
// Without it, compensation inherits the step policy given by WithRetry. The
// reason it can be given separately is that the two sides cost different
// things: a failed Invoke costs the execution being rolled back, while a failed
// Compensate costs A HUMAN'S TIME. "Try the step once but insist on the
// compensation" is therefore a legitimate configuration.
func WithCompensationRetry(p RetryPolicy) RunOption {
	return func(o *runOptions) error {
		np, err := p.normalize("WithCompensationRetry")
		if err != nil {
			return err
		}
		o.compensationRetry = np
		o.compensationRetrySet = true

		return nil
	}
}

// WithLease declares the longest an execution may LEGITIMATELY take.
//
// # Why it is needed
//
// An execution record is opened "running" and closes by moving to a terminal
// state. If the process dies before it can write that transition — a deploy, an
// OOM, a pod eviction, a crash — the record stays running FOREVER. The engine's
// repeat logic looks at it and says "still running", and every call with the
// same key gets a 409. Measured: an execution that had crashed three days
// earlier still said "running", and that cart could never be paid for again.
//
// Age alone is not proof; the LEASE is. If the caller already gives the flow a
// finite budget (the cart flow's is two minutes), a record that has been
// "running" for longer than that budget is a record no process can be holding.
// That is why the duration is not guessed by the engine but DECLARED by the
// caller.
//
// # What is done
//
// A record whose lease has expired does not count as "running"; what it did is
// determined from the step records (see [Executor.Run]):
//
//   - If no step did any work there is nothing to compensate: the record
//     becomes [StatusFailed], releases its key, and the caller can RETRY.
//   - If work was done, compensation never ran and half-done work is out there:
//     the record becomes [StatusCompensationFailed], KEEPS its key and says a
//     human is needed. Retrying in silence would have meant reserving the
//     already-reserved stock a second time.
//
// A zero or negative value TURNS this behavior OFF: a caller that declares no
// lease gets the old behavior.
func WithLease(d time.Duration) RunOption {
	return func(o *runOptions) error {
		o.lease = d

		return nil
	}
}

// WithCompensationTimeout gives each compensation call a time budget.
//
// The budget is PER STEP: every Compensate gets its own time and one step's slow
// compensation does not eat the budget of the steps before it. The chain's total
// time is at worst the step count × the budget; that is a deliberate trade,
// because the alternative (a single shared budget) meant calling the earliest
// step — typically the one holding the heaviest resource — with a dead context.
// A step's retried compensations share this budget too. The default is
// DefaultCompensationTimeout. Zero or negative cannot be given: a compensation
// without a budget hangs indefinitely on a dead dependency.
func WithCompensationTimeout(d time.Duration) RunOption {
	return func(o *runOptions) error {
		if d <= 0 {
			return errors.Invalid(CodeInvalidOption,
				"the compensation timeout has to be positive, %s was given", d)
		}
		o.compensationTimeout = d

		return nil
	}
}

// WithStoreTimeout gives a single Store call a time budget.
//
// The default is DefaultStoreTimeout. Zero or negative cannot be given.
func WithStoreTimeout(d time.Duration) RunOption {
	return func(o *runOptions) error {
		if d <= 0 {
			return errors.Invalid(CodeInvalidOption,
				"the store timeout has to be positive, %s was given", d)
		}
		o.storeTimeout = d

		return nil
	}
}

// newRunOptions applies the options in order and returns the validated settings.
func newRunOptions(opts []RunOption) (*runOptions, error) {
	o := &runOptions{
		retry:               NoRetry(),
		compensationRetry:   NoRetry(),
		compensationTimeout: DefaultCompensationTimeout,
		storeTimeout:        DefaultStoreTimeout,
	}

	for _, opt := range opts {
		if opt == nil {
			return nil, errors.Invalid(CodeInvalidOption, "a nil RunOption cannot be given")
		}
		if err := opt(o); err != nil {
			return nil, err
		}
	}

	if !o.compensationRetrySet {
		o.compensationRetry = o.retry
	}

	return o, nil
}

// storeContext produces a time-bounded context for Store calls that is NOT
// AFFECTED by cancellation.
//
// The execution's trace has to be writable even when the caller's context was
// canceled: tying persistence to cancellation would leave the record empty
// exactly when the trace is needed most (a canceled, compensated execution).
// There is still a budget; an unreachable database cannot hang the execution
// indefinitely.
func (o *runOptions) storeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), o.storeTimeout)
}
