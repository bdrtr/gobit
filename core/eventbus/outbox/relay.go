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

// selectPendingSQL reads the oldest unpublished events.
//
// FOR UPDATE SKIP LOCKED is what lets more than one instance relay at once:
// each takes the rows it locked and steps over the ones another is already
// sending, so adding a replica adds throughput rather than duplicates. Without
// it two relays would read the same rows and publish each event twice.
const selectPendingSQL = `
SELECT id, name, data, attempts, created_at
FROM event_outbox
WHERE published_at IS NULL
ORDER BY created_at, id
LIMIT $1
FOR UPDATE SKIP LOCKED`

// markPublishedSQL closes a row.
const markPublishedSQL = `
UPDATE event_outbox
SET published_at = now(), last_error = ''
WHERE id = $1`

// markFailedSQL records an attempt that did not land.
const markFailedSQL = `
UPDATE event_outbox
SET attempts = attempts + 1, last_error = $2
WHERE id = $1`

// Store reads and closes outbox rows.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds a store over the pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Relay hands the pending events to publish, one transaction for the batch.
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
// It reports how many were published and how many failed. A pass that
// published nothing and failed everything is not the same as an empty one, and
// a caller that could not tell them apart would log "nothing to do" during an
// outage.
func (s *Store) Relay(
	ctx context.Context, limit int32, publish func(context.Context, Pending) error,
) (published, failed int, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, 0, errors.Wrap(err, errors.KindInternal, CodeReadFailed,
			"the outbox relay could not begin a transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	pending, err := readPending(ctx, tx, limit)
	if err != nil {
		return 0, 0, err
	}

	for _, event := range pending {
		if publishErr := publish(ctx, event); publishErr != nil {
			if _, execErr := tx.Exec(ctx, markFailedSQL, event.ID, publishErr.Error()); execErr != nil {
				return published, failed, errors.Wrap(execErr, errors.KindInternal, CodeReadFailed,
					"the failed attempt on event %s could not be recorded", event.ID)
			}
			failed++

			continue
		}

		if _, execErr := tx.Exec(ctx, markPublishedSQL, event.ID); execErr != nil {
			// The event WENT OUT and the row could not be closed. Failing the
			// whole pass here is correct: the alternative is committing the
			// rest and leaving this one to be published a second time on the
			// next pass with nothing saying why.
			return published, failed, errors.Wrap(execErr, errors.KindInternal, CodeReadFailed,
				"event %s was published but could not be marked; it will be sent again", event.ID)
		}
		published++
	}

	if err := tx.Commit(ctx); err != nil {
		return published, failed, errors.Wrap(err, errors.KindInternal, CodeReadFailed,
			"the outbox relay could not commit")
	}

	return published, failed, nil
}

// readPending reads and locks the oldest unpublished rows.
func readPending(ctx context.Context, tx pgx.Tx, limit int32) ([]Pending, error) {
	rows, err := tx.Query(ctx, selectPendingSQL, limit)
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
