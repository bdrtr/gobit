package outbox

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
)

// CodeReadFailed reports that the pending events could not be read.
const CodeReadFailed = "outbox_read_failed"

// selectDueSQL reads the oldest events that are DUE.
//
// FOR UPDATE SKIP LOCKED is what lets more than one instance relay at once:
// each takes the rows it locked and steps over the ones another is already
// sending, so adding a replica adds throughput rather than duplicates. Without
// it two relays would read the same rows and publish each event twice.
//
// # Why the two extra predicates are not cosmetic
//
// `dead_lettered_at IS NULL` is the fix for a measured outage shape, not a
// tidy-up. The relay reads the OLDEST pending rows up to its limit; a limit's
// worth of permanently failing rows therefore fills every batch, and measured
// against a real PostgreSQL with the previous version of this query, five
// consecutive passes published nothing while a healthy event written behind
// them ended with `attempts = 0` — never attempted once. Excluding the rows
// the relay has given up on is what lets the queue move again.
//
// `next_attempt_at <= now()` is the same property one step earlier: a row that
// is merely failing, rather than dead, also stops occupying a slot in every
// single pass while its backoff runs.
//
// now() is the DATABASE's clock deliberately. The instant is written by the
// same statement that fails an attempt, so both sides of the comparison come
// from one clock; taking the deadline from the application's would make the
// backoff depend on the skew between the two.
const selectDueSQL = `
SELECT id, name, data, attempts, created_at
FROM event_outbox
WHERE published_at IS NULL
  AND dead_lettered_at IS NULL
  AND next_attempt_at <= now()
ORDER BY created_at, id
LIMIT $1
FOR UPDATE SKIP LOCKED`

// markPublishedSQL closes a row.
const markPublishedSQL = `
UPDATE event_outbox
SET published_at = now(), last_error = ''
WHERE id = $1`

// markFailedSQL records an attempt that did not land, and gives up if this was
// the last one allowed.
//
// The decision is taken HERE, in the same statement that counts the attempt,
// and that is what makes it correct under concurrency: two relays that somehow
// both counted this row cannot disagree about whether it crossed the ceiling,
// because neither reads the counter into Go to compare it. The comparison is
// `attempts + 1` — the value being written — against the policy's ceiling.
//
// dead_lettered_at is set to now() and never cleared here: a row already dead
// keeps the instant it died, so raising the ceiling later cannot rewrite the
// history a human was shown. [Store.Redrive] is the only way back.
//
// The interval comes from make_interval rather than string concatenation
// because the delay is a parameter, and a delay pasted into SQL text is the
// one place in this file where a value could become syntax.
const markFailedSQL = `
UPDATE event_outbox
SET attempts = attempts + 1,
    last_error = $2,
    next_attempt_at = now() + make_interval(secs => $3),
    dead_lettered_at = CASE
        WHEN attempts + 1 >= $4 THEN now()
        ELSE dead_lettered_at
    END
WHERE id = $1
RETURNING dead_lettered_at IS NOT NULL`

// Store reads and closes outbox rows.
type Store struct {
	pool   *pgxpool.Pool
	policy Policy
}

// NewStore builds a store over the pool, with [DefaultPolicy].
func NewStore(pool *pgxpool.Pool) *Store { return NewStoreWithPolicy(pool, DefaultPolicy()) }

// NewStoreWithPolicy builds a store that retries and gives up on the caller's
// terms.
//
// It is a second constructor rather than an argument on the first, and the
// reason is that the composition root has no opinion about retries: an
// installation that wants the defaults should not have to name them, and a
// signature change there would reach every embedder for a parameter almost
// none of them will set. A zero field in the policy means "not chosen" — see
// [Policy] — so a partial literal is safe.
func NewStoreWithPolicy(pool *pgxpool.Pool, policy Policy) *Store {
	return &Store{pool: pool, policy: policy.normalized()}
}

// RelayResult is what one pass did.
//
// A pass that published nothing and failed everything is not the same as an
// empty one, and a caller that could not tell them apart would log "nothing to
// do" during an outage. The dead letters are carried as IDS rather than a
// count because the moment a row is given up on is the only moment anything
// knows which row it was: the caller's log line is the record.
type RelayResult struct {
	// Published is how many events reached the bus.
	Published int
	// Failed is how many did not. It INCLUDES the ones that crossed the
	// ceiling in this pass — those failed and were then given up on.
	Failed int
	// DeadLettered are the ids the relay stopped trying, in this pass only.
	DeadLettered []string
}

// Relay hands the due events to publish, one transaction for the batch.
//
// # Why the whole batch runs in ONE transaction
//
// The rows are locked with SKIP LOCKED, and the lock lives until the
// transaction ends. Holding it while each event is published is what stops a
// second relay from picking up an event that is in flight — the lock IS the
// claim, so there is nothing else to expire and nothing to reap.
//
// # Why a publish failure does not roll the batch back
//
// The successes are marked inside the same transaction and the failures only
// have their attempt counted, so a batch where one event fails still closes the
// others. Rolling back would re-send every event that already went out.
//
// # What a failure costs the event, and what it does not
//
// The row is not retried immediately and it is not retried forever. It waits
// out a growing delay ([Policy]) and, after the policy's ceiling, it is
// dead-lettered: it stops being selected and becomes readable through
// [Store.DeadLetters]. Neither of the two things an outbox must never do —
// spinning on a poisoned event, or losing one quietly — is available to it.
//
// # Ordering
//
// This weakens nothing, because there was nothing to weaken. The bus's own
// contract says plainly that NEITHER backend guarantees delivery order, and
// the previous version of this relay already delivered a later event before an
// earlier one whenever the earlier one failed and the later one did not — in
// the same batch. What backoff changes is the SIZE of that window, from a
// minute to hours. Handlers were already required to be idempotent and
// order-independent; this makes the requirement expensive to ignore rather
// than newly true.
func (s *Store) Relay(
	ctx context.Context, limit int32, publish func(context.Context, Pending) error,
) (RelayResult, error) {
	var result RelayResult

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, errors.Wrap(err, errors.KindInternal, CodeReadFailed,
			"the outbox relay could not begin a transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	pending, err := readDue(ctx, tx, limit)
	if err != nil {
		return result, err
	}

	for _, event := range pending {
		if publishErr := publish(ctx, event); publishErr != nil {
			dead, execErr := s.recordFailure(ctx, tx, event, publishErr)
			if execErr != nil {
				return result, execErr
			}
			result.Failed++
			if dead {
				result.DeadLettered = append(result.DeadLettered, event.ID)
			}

			continue
		}

		if _, execErr := tx.Exec(ctx, markPublishedSQL, event.ID); execErr != nil {
			// The event WENT OUT and the row could not be closed. Failing the
			// whole pass here is correct: the alternative is committing the
			// rest and leaving this one to be published a second time on the
			// next pass with nothing saying why.
			return result, errors.Wrap(execErr, errors.KindInternal, CodeReadFailed,
				"event %s was published but could not be marked; it will be sent again", event.ID)
		}
		result.Published++
	}

	if err := tx.Commit(ctx); err != nil {
		return result, errors.Wrap(err, errors.KindInternal, CodeReadFailed,
			"the outbox relay could not commit")
	}

	return result, nil
}

// recordFailure counts the attempt, schedules the next one and reports whether
// this was the attempt that gave up.
//
// The delay is computed from the count the row will HAVE after this statement,
// not the one it had before: the first failure of a fresh row waits
// Policy.FirstDelay rather than nothing.
func (s *Store) recordFailure(
	ctx context.Context, tx pgx.Tx, event Pending, cause error,
) (dead bool, err error) {
	delay := s.policy.delayAfter(event.Attempts + 1)

	if scanErr := tx.QueryRow(ctx, markFailedSQL,
		event.ID, cause.Error(), delay.Seconds(), s.policy.MaxAttempts,
	).Scan(&dead); scanErr != nil {
		return false, errors.Wrap(scanErr, errors.KindInternal, CodeReadFailed,
			"the failed attempt on event %s could not be recorded", event.ID)
	}

	return dead, nil
}

// readDue reads and locks the oldest rows that are due.
func readDue(ctx context.Context, tx pgx.Tx, limit int32) ([]Pending, error) {
	rows, err := tx.Query(ctx, selectDueSQL, limit)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeReadFailed,
			"the pending outbox events could not be read")
	}
	defer rows.Close()

	out := make([]Pending, 0, limit)
	for rows.Next() {
		var (
			event   Pending
			payload []byte
		)
		if err := rows.Scan(&event.ID, &event.Name, &payload, &event.Attempts, &event.CreatedAt); err != nil {
			return nil, errors.Wrap(err, errors.KindInternal, CodeReadFailed,
				"an outbox row could not be read")
		}
		if err := json.Unmarshal(payload, &event.Data); err != nil {
			return nil, errors.Wrap(err, errors.KindInternal, CodeReadFailed,
				"the payload of outbox event %s could not be decoded", event.ID)
		}
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeReadFailed,
			"the pending outbox events could not be walked")
	}

	return out, nil
}
