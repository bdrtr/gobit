package webhookout

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// The id prefixes. They earn their place twice over: an id appears in an admin
// URL, and an operator reading a log line has to be able to tell a receiver
// from the delivery owed to it without counting characters.
const (
	// endpointPrefix marks a registered receiver.
	endpointPrefix = "whe_"
	// deliveryPrefix marks one delivery owed to one receiver.
	deliveryPrefix = "whd_"
)

// idBodyLength is the number of random characters after a prefix.
const idBodyLength = 26

// idAlphabet is Crockford Base32 without the letters that misread.
const idAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// secretBytes is the length of a generated endpoint secret.
//
// Thirty-two bytes, which is the block size of the SHA-256 the MAC is built on.
// A shorter key would still produce a valid HMAC and would be the weakest link
// in a scheme whose only credential it is.
const secretBytes = 32

// Error codes.
const (
	codeStoreFailed = "webhookout_store_failed"
	codeNotFound    = "webhookout_not_found"
)

// topicFilters is, per topic, the payload fields an event must carry one of
// the listed values in (ADR 0218).
type topicFilters map[string]map[string][]string

// topicFields is, per topic, the payload fields a receiver is sent.
type topicFields map[string][]string

// endpoint is one registered receiver.
type endpoint struct {
	ID          string
	URL         string
	Secret      string
	Topics      []string
	Filters     topicFilters
	Fields      topicFields
	Description string
	// MaxPerMinute is how many of its deliveries are leased in a clock minute
	// at most, by every claim together; zero is no cap (ADR 0275, ADR 0436).
	MaxPerMinute int64
	CreatedAt    time.Time
}

// rateColumn is a receiver's cap as the column holds it: NULL for none.
func rateColumn(maxPerMinute int64) *int64 {
	if maxPerMinute <= 0 {
		return nil
	}

	return &maxPerMinute
}

// rateOf reads the column back: zero for none.
func rateOf(column *int64) int64 {
	if column == nil {
		return 0
	}

	return *column
}

// delivery is one delivery owed to one receiver.
type delivery struct {
	ID         string
	EndpointID string
	URL        string
	Secret     string
	EventID    string
	EventName  string
	OccurredAt time.Time
	Payload    map[string]any
	Redacted   []string
	// Attempts is the count BEFORE this attempt, so the attempt about to be
	// made is Attempts+1. The header the receiver sees is 1-based.
	Attempts int64
	// LeasedUntil is the next_attempt_at the claim wrote, which is what
	// [store.release] compares before it hands the row back: a lease that has
	// changed since is no longer this pass's to release.
	LeasedUntil time.Time
}

// deadLetter is one delivery the sender gave up on.
type deadLetter struct {
	ID         string
	EndpointID string
	URL        string
	EventName  string
	EventID    string
	Attempts   int64
	LastError  string
	LastStatus int
	CreatedAt  time.Time
	DeadAt     time.Time
}

// deadLetterReport is the pile, and a sample of it.
//
// Count is the whole pile and Oldest is the part that fits. They are separate
// for the reason the outbox's report gives: a report showing only the sample
// answers "what broke?" and not "how much?", and during an outage the second
// question is the one that decides whether anybody is woken up.
type deadLetterReport struct {
	Count  int64
	Oldest []deadLetter
}

// empty reports whether there is nothing for a human to look at.
func (r deadLetterReport) empty() bool { return r.Count == 0 }

// store is the plugin's data access. It owns exactly two tables.
type store struct {
	pool *pgxpool.Pool
}

// newStore builds the store over the core pool.
func newStore(pool *pgxpool.Pool) *store { return &store{pool: pool} }

// createEndpoint registers a receiver and returns the row, secret included.
//
// The secret is minted HERE rather than accepted from the caller. An operator
// pasting their own would eventually paste a short one, and the surface that
// accepts a weak key is the surface that gets one.
func (s *store) createEndpoint(
	ctx context.Context, url string, topics []string, filters topicFilters, fields topicFields, description string,
	maxPerMinute int64,
) (endpoint, error) {
	id, err := newID(endpointPrefix)
	if err != nil {
		return endpoint{}, err
	}

	secret, err := newSecret()
	if err != nil {
		return endpoint{}, err
	}

	if filters == nil {
		filters = topicFilters{}
	}
	if fields == nil {
		fields = topicFields{}
	}
	row := endpoint{
		ID: id, URL: url, Secret: secret, Topics: topics, Filters: filters, Fields: fields, Description: description,
		MaxPerMinute: maxPerMinute,
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO webhook_endpoint (id, url, secret, topics, filters, fields, description, max_per_minute)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at`,
		id, url, secret, topics, filters, fields, description, rateColumn(maxPerMinute)).Scan(&row.CreatedAt)
	if err != nil {
		return endpoint{}, wrapDB(err, "the webhook endpoint could not be registered")
	}

	return row, nil
}

// listEndpoints reads every registered receiver, oldest first.
//
// The secret is NOT selected. A listing that returned it would put the signing
// key of every integration into any log, proxy or screen recording that touched
// an admin response, and the only thing needing it is the sender.
func (s *store) listEndpoints(ctx context.Context) ([]endpoint, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, url, topics, filters, fields, description, max_per_minute, created_at
		FROM webhook_endpoint
		ORDER BY created_at, id`)
	if err != nil {
		return nil, wrapDB(err, "the webhook endpoints could not be listed")
	}
	defer rows.Close()

	var out []endpoint
	for rows.Next() {
		var (
			e    endpoint
			rate *int64
		)
		if err := rows.Scan(&e.ID, &e.URL, &e.Topics, &e.Filters, &e.Fields, &e.Description, &rate,
			&e.CreatedAt); err != nil {
			return nil, wrapDB(err, "a webhook endpoint row could not be read")
		}
		e.MaxPerMinute = rateOf(rate)
		out = append(out, e)
	}

	return out, wrapDB(rows.Err(), "the webhook endpoint rows could not be read")
}

// updateEndpoint rewrites a receiver's topics, filters, fields and description
// under its row lock (ADR 0218): change is handed the row as it is and returns
// it as it is to be, or an error that writes nothing. The URL and the secret are
// not changed, and the deliveries already written stay as they were written.
// A receiver that is not there is (endpoint{}, false, nil).
func (s *store) updateEndpoint(
	ctx context.Context, id string, change func(current endpoint) (endpoint, error),
) (endpoint, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return endpoint{}, false, wrapDB(err, "the webhook endpoint could not be read")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current := endpoint{ID: id}
	var rate *int64
	err = tx.QueryRow(ctx, `
		SELECT url, topics, filters, fields, description, max_per_minute, created_at
		FROM webhook_endpoint WHERE id = $1 FOR UPDATE`, id).
		Scan(&current.URL, &current.Topics, &current.Filters, &current.Fields, &current.Description, &rate,
			&current.CreatedAt)
	current.MaxPerMinute = rateOf(rate)
	if errors.Is(err, pgx.ErrNoRows) {
		return endpoint{}, false, nil
	}
	if err != nil {
		return endpoint{}, false, wrapDB(err, "the webhook endpoint could not be read")
	}
	next, err := change(current)
	if err != nil {
		return endpoint{}, true, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE webhook_endpoint
		SET topics = $2, filters = $3, fields = $4, description = $5, max_per_minute = $6, updated_at = now()
		WHERE id = $1`, id, next.Topics, next.Filters, next.Fields, next.Description,
		rateColumn(next.MaxPerMinute)); err != nil {
		return endpoint{}, true, wrapDB(err, "the webhook endpoint could not be updated")
	}
	if err := tx.Commit(ctx); err != nil {
		return endpoint{}, true, wrapDB(err, "the webhook endpoint could not be updated")
	}
	next.ID, next.URL, next.CreatedAt = id, current.URL, current.CreatedAt

	return next, true, nil
}

// deleteEndpoint removes a receiver and reports whether there was one.
//
// The deliveries owed to it are deliberately LEFT: see the migration for why
// there is no cascade. A dead letter whose receiver has been removed is still
// the record of an event that was never sent.
func (s *store) deleteEndpoint(ctx context.Context, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM webhook_endpoint WHERE id = $1`, id)
	if err != nil {
		return false, wrapDB(err, "the webhook endpoint could not be deleted")
	}

	return tag.RowsAffected() > 0, nil
}

// enqueueSQL writes one delivery per receiver that asked for this topic, in one
// statement.
//
// # Why the fan-out is a SELECT and not a loop
//
// The subscriber runs on the event bus, where a handler's error reaches nobody
// but the log. A loop that read the receivers and then inserted would have a
// window between the two in which a receiver registered a moment ago is missed
// — silently, and only for that event. One statement closes it: the set of
// receivers and the rows written from it come from the same snapshot.
//
// ON CONFLICT DO NOTHING is what makes the subscriber idempotent. The bus
// delivers at least once and the outbox relay can publish an event the direct
// publish already delivered; both arrive with the same event id, and the unique
// (endpoint_id, event_id) turns the second one into a no-op instead of a second
// POST.
//
// The id is generated in SQL rather than in Go because the number of rows is
// not known until the SELECT runs. It is the same alphabet and length the Go
// side uses, built from gen_random_uuid()'s hex — a decision worth naming
// because two id shapes for one column would make the prefix meaningless.
//
// # Filters and fields (ADR 0218)
//
// A receiver's filter for the topic is read in the same statement: every field
// it names has to carry one of its values, as text, and an event that does not
// writes no row for that receiver. Its field list for the topic, when it has
// one, is applied to the row's payload, so what is queued is what is sent and a
// redrive sends the same body.
const enqueueSQL = `
INSERT INTO webhook_delivery (
    id, endpoint_id, url, event_id, event_name, occurred_at, payload, redacted
)
SELECT $1 || upper(translate(substr(gen_random_uuid()::text, 1, 26), '-', '0')),
       e.id, e.url, $2, $3, $4,
       CASE WHEN e.fields ? $3 THEN (
           SELECT COALESCE(jsonb_object_agg(p.key, p.value), '{}'::jsonb)
           FROM jsonb_each($5::jsonb) AS p
           WHERE e.fields -> $3 ? p.key
       ) ELSE $5::jsonb END,
       $6
FROM webhook_endpoint e
WHERE e.topics @> ARRAY[$3]::text[]
  AND NOT EXISTS (
      SELECT 1 FROM jsonb_each(COALESCE(e.filters -> $3, '{}'::jsonb)) AS f
      WHERE NOT (f.value ? ($5::jsonb ->> f.key))
         OR ($5::jsonb ->> f.key) IS NULL
  )
ON CONFLICT (endpoint_id, event_id) DO NOTHING`

// enqueue writes the deliveries this event owes and returns how many were new.
func (s *store) enqueue(
	ctx context.Context, eventID, eventName string, occurredAt time.Time,
	payload map[string]any, redacted []string,
) (int64, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	if redacted == nil {
		redacted = []string{}
	}

	tag, err := s.pool.Exec(ctx, enqueueSQL,
		deliveryPrefix, eventID, eventName, occurredAt.UTC(), payload, redacted)
	if err != nil {
		return 0, wrapDB(err, "the webhook deliveries could not be enqueued")
	}

	return tag.RowsAffected(), nil
}

// claimSQL takes the due deliveries and LEASES them.
//
// # Why a lease and not a held transaction
//
// The outbox relay reads its batch `FOR UPDATE SKIP LOCKED` and publishes
// inside that transaction. That is right for the outbox, whose publish is a
// local bus call measured in microseconds. It is wrong here: an attempt is an
// HTTP request to a third party with a timeout of [perAttemptTimeout], and a
// transaction held open across a pass of them would hold a pool connection and
// a snapshot for most of a minute — against a receiver that has stopped
// answering, which is precisely when the pass runs long.
//
// So the transaction covers only the claim. It moves next_attempt_at forward by
// [claimLease], which makes the rows invisible to another claim, and then the
// requests happen with nothing held. The properties this trades for:
//
//   - A process that dies mid-pass leaves its rows leased rather than locked.
//     They come back on their own when the lease elapses, instead of the
//     instant the connection drops. That is the cost, and it is bounded by one
//     lease.
//   - A row claimed and never closed is retried WITHOUT its attempt counted.
//     That is the safe direction: the alternative counts an attempt that was
//     never made and brings the dead letter closer for a receiver that did
//     nothing wrong.
//
// # One claim at a time (ADR 0436)
//
// Every serving process claims — its worker and the minute job — so claims
// meet as a matter of course, and SKIP LOCKED alone does not keep them apart.
// A claim whose snapshot predates another's commit, and that reaches a row
// after it, finds the row updated rather than locked: it locks the new version
// and rechecks only the scan's own condition, the id, never the due condition
// the snapshot already decided, and leases the row a second time (D275). So
// [store.claimDue] takes [claimLockKey] before this statement, and the
// statement's snapshot is taken after the claim before it committed.
//
// # One instant, read once the lock is held
//
// $3 is the claim's instant: what is due, the lease and the minute a cap
// counts in are all measured from it. [store.claimDue] reads it after the lock
// is granted, so the claims' instants run in the order they hold the lock. The
// transaction's now() would not do: it is BEGIN's instant, before the wait for
// the lock, and a claim that began just before a minute's end and took the
// lock after one from the next minute counted in the older minute and reset
// the receiver's window (measurements/0436).
//
// # A receiver's rate (ADR 0275, ADR 0436)
//
// A receiver that set max_per_minute is leased at most that many of its due
// deliveries in a clock minute, by every claim together, oldest first. The
// count is the receiver's own row: rate_claimed is what was leased in the
// minute rate_window names, room is the cap less that count while the window
// is this minute or later and the whole cap once it is older, the window
// numbers each receiver's due rows and the claim takes only the ones inside
// its room, and the counted CTE adds what was leased and never moves the
// window back. The rest are not leased, so they wait with no attempt counted.
// A row another process holds is skipped, and a leased row released
// unattempted stays counted, which can leave a receiver under its cap for a
// minute and never over it.
//
// What is capped is the LEASE. A pass sends what it leased within [maxRun], so
// rows leased at the end of one minute can arrive in the next one beside that
// minute's own: across a minute's boundary a receiver can be sent up to twice
// its cap within sixty seconds, and never more than its cap leased in one
// clock minute.
//
// # Receivers take turns (ADR 0436)
//
// The claim takes each receiver's oldest due row before any receiver's second,
// and within a turn a row never attempted before one that has failed: place is
// a receiver's rank by age, and both the LIMIT and the order the rows come
// back in are by place, then attempts, then age. A pass attempts its rows in
// that order, so a receiver whose requests hang until their timeout, and whose
// retries keep their age, fills only its own turns: a fresh delivery to
// another receiver is in the claim's first turn and among its first attempts.
// Oldest first across all receivers let such a backlog hold every other
// receiver's first attempt for minutes (measurements/0436). The same place
// decides a capped receiver's room, so the rows inside its cap are its first
// turns.
const claimSQL = `
WITH clock AS (
    SELECT date_trunc('minute', $3::timestamptz) AS minute
), room AS (
    SELECT e.id,
           CASE WHEN e.max_per_minute IS NULL THEN NULL
                WHEN e.rate_window >= c.minute THEN e.max_per_minute - e.rate_claimed
                ELSE e.max_per_minute
           END AS left_now
    FROM webhook_endpoint e
    CROSS JOIN clock c
), due AS (
    SELECT d.id, r.left_now,
           row_number() OVER (PARTITION BY d.endpoint_id ORDER BY d.created_at, d.id) AS place
    FROM webhook_delivery d
    JOIN room r ON r.id = d.endpoint_id
    WHERE d.delivered_at IS NULL
      AND d.dead_lettered_at IS NULL
      AND d.next_attempt_at <= $3::timestamptz
      AND (r.left_now IS NULL OR r.left_now > 0)
), allowed AS (
    SELECT d.id, u.place
    FROM webhook_delivery d
    JOIN due u ON u.id = d.id
    WHERE u.left_now IS NULL OR u.place <= u.left_now
    ORDER BY u.place, d.attempts, d.created_at, d.id
    LIMIT $1
    FOR UPDATE OF d SKIP LOCKED
), leased AS (
    UPDATE webhook_delivery d
    SET next_attempt_at = $3::timestamptz + make_interval(secs => $2)
    FROM allowed
    WHERE d.id = allowed.id
    RETURNING d.id, d.endpoint_id, d.url, d.event_id, d.event_name, d.occurred_at,
              d.payload, d.redacted, d.attempts, d.next_attempt_at, d.created_at, allowed.place
), counted AS (
    UPDATE webhook_endpoint e
    SET rate_claimed = CASE WHEN e.rate_window >= c.minute THEN e.rate_claimed ELSE 0 END + n.claimed,
        rate_window = GREATEST(e.rate_window, c.minute)
    FROM (SELECT endpoint_id, count(*) AS claimed FROM leased GROUP BY endpoint_id) n
    CROSS JOIN clock c
    WHERE e.id = n.endpoint_id AND e.max_per_minute IS NOT NULL
)
SELECT l.id, l.endpoint_id, l.url, e.secret, l.event_id, l.event_name,
       l.occurred_at, l.payload, l.redacted, l.attempts, l.next_attempt_at
FROM leased l
JOIN webhook_endpoint e ON e.id = l.endpoint_id
ORDER BY l.place, l.attempts, l.created_at, l.id`

// claimLockTimeoutSQL bounds the wait for [claimLockKey]: a claim takes
// milliseconds, so five seconds behind one is a stuck backend, and the claim
// fails and is tried again rather than holding a pooled connection behind it.
// lock_timeout bounds an advisory lock's wait as it does a row's.
const claimLockTimeoutSQL = `SET LOCAL lock_timeout = '5s'`

// claimLockSQL takes the claim lock until the claim's transaction ends.
const claimLockSQL = `SELECT pg_advisory_xact_lock($1)`

// claimInstantSQL is the claim's instant, read in a statement of its own once
// the lock is held: statement_timestamp() is when that statement arrived, after
// the lock statement returned, where now() is the transaction's BEGIN.
const claimInstantSQL = `SELECT statement_timestamp()`

// claimLockClass is the class of the claim lock, the upper 32 bits of its key;
// internal/arch holds every class in the tree apart.
const claimLockClass int64 = 9

// claimLockKey serializes the claims, so that each one's snapshot carries the
// leases and the counts of the claims before it (ADR 0436, D275).
const claimLockKey int64 = claimLockClass << 32

// claimDue leases up to limit due deliveries and returns them.
//
// A delivery whose endpoint row is GONE is not returned, because the join finds
// no secret to sign with. It is not an oversight and it is not silent: such a
// row can never be sent again, it keeps its place in the due index, and
// [store.orphanCount] is what reports it — the delivery job says so on every
// pass rather than leaving a row that is due forever and never picked up.
//
// It also returns the claim's instant, by the database's clock: every row due
// by then that the claim could take, it took, which is what the worker's next
// wake is measured from ([store.nextWake]).
func (s *store) claimDue(ctx context.Context, limit int32) ([]delivery, time.Time, error) {
	return s.claimDueAt(ctx, limit, nil)
}

// claimDueAt is [store.claimDue] at a named instant: nil reads the claim's
// instant from the database once the lock is held, and only a test names one,
// to put a claim in a minute of its choosing.
func (s *store) claimDueAt(
	ctx context.Context, limit int32, at *time.Time,
) ([]delivery, time.Time, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, time.Time{}, wrapDB(err, "the due webhook deliveries could not be claimed")
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, claimLockTimeoutSQL); err != nil {
		return nil, time.Time{}, wrapDB(err, "the due webhook deliveries could not be claimed")
	}
	if _, err := tx.Exec(ctx, claimLockSQL, claimLockKey); err != nil {
		return nil, time.Time{}, wrapDB(err, "the webhook claim lock could not be taken")
	}
	var instant time.Time
	if at != nil {
		instant = *at
	} else if err := tx.QueryRow(ctx, claimInstantSQL).Scan(&instant); err != nil {
		return nil, time.Time{}, wrapDB(err, "the webhook claim's instant could not be read")
	}

	rows, err := tx.Query(ctx, claimSQL, limit, claimLease.Seconds(), instant)
	if err != nil {
		return nil, time.Time{}, wrapDB(err, "the due webhook deliveries could not be claimed")
	}

	var out []delivery
	for rows.Next() {
		var d delivery
		if err := rows.Scan(&d.ID, &d.EndpointID, &d.URL, &d.Secret, &d.EventID,
			&d.EventName, &d.OccurredAt, &d.Payload, &d.Redacted, &d.Attempts, &d.LeasedUntil); err != nil {
			rows.Close()

			return nil, time.Time{}, wrapDB(err, "a webhook delivery row could not be read")
		}
		out = append(out, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, wrapDB(err, "the webhook delivery rows could not be read")
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, time.Time{}, wrapDB(err, "the webhook claim could not be committed")
	}

	return out, instant, nil
}

// releaseSQL hands leased rows back, due at once, while each lease is still
// the one its claim wrote.
const releaseSQL = `
UPDATE webhook_delivery d
SET next_attempt_at = now()
FROM unnest($1::text[], $2::timestamptz[]) AS r(id, leased_until)
WHERE d.id = r.id
  AND d.next_attempt_at = r.leased_until
  AND d.delivered_at IS NULL
  AND d.dead_lettered_at IS NULL`

// release hands back the rows a pass claimed and did not attempt, so the next
// claim sees them rather than the one after the lease (D274).
//
// It compares each lease with the one the claim wrote, so a row whose lease
// elapsed and that another claim took since is left to that claim.
func (s *store) release(ctx context.Context, rows []delivery) error {
	if len(rows) == 0 {
		return nil
	}

	ids := make([]string, len(rows))
	leases := make([]time.Time, len(rows))
	for i := range rows {
		ids[i], leases[i] = rows[i].ID, rows[i].LeasedUntil
	}

	if _, err := s.pool.Exec(ctx, releaseSQL, ids, leases); err != nil {
		return wrapDB(err, "the unattempted webhook deliveries could not be released")
	}

	return nil
}

// nextWakeSQL is the instant the next pending delivery falls due after the
// worker's last claim, and the database's own now beside it.
//
// It reads only rows due AFTER that claim's instant ($1). A row due by then
// that the claim did not take is over its receiver's cap or held by another
// process, and a worker that woke for it would wake again at once. A row that
// fell due after the claim — while the pass was sending — is read, and if it
// is due already the wait is not positive and the worker claims again at once
// (ADR 0436, D273). A receiver's next minute comes to the minute job, or to a
// worker within [every].
const nextWakeSQL = `
SELECT min(d.next_attempt_at), now()
FROM webhook_delivery d
WHERE d.delivered_at IS NULL
  AND d.dead_lettered_at IS NULL
  AND d.next_attempt_at > $1
  AND EXISTS (SELECT 1 FROM webhook_endpoint e WHERE e.id = d.endpoint_id)`

// nextWake reports how long until the first pending delivery due after the
// instant claimed falls due, and false when there is none. The wait is zero or
// less when that delivery is due already.
//
// The wait is the difference of two instants read from the DATABASE's clock in
// one statement, so a server whose clock runs ahead of the database's cannot
// wake before the row is due there.
func (s *store) nextWake(ctx context.Context, claimed time.Time) (time.Duration, bool, error) {
	var (
		earliest *time.Time
		now      time.Time
	)
	if err := s.pool.QueryRow(ctx, nextWakeSQL, claimed).Scan(&earliest, &now); err != nil {
		return 0, false, wrapDB(err, "the next due webhook delivery could not be read")
	}
	if earliest == nil {
		return 0, false, nil
	}

	return earliest.Sub(now), true, nil
}

// orphanCount reports how many due deliveries name an endpoint that is gone.
//
// It exists because [claimSQL]'s join makes those rows unreachable, and an
// unreachable row that is nevertheless due would otherwise be invisible: it
// occupies the due index forever, contributes to no report and is delivered to
// nobody. Counting it is what turns it into something an operator can act on
// (redrive is useless; discard is the answer).
func (s *store) orphanCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM webhook_delivery d
		WHERE d.delivered_at IS NULL
		  AND d.dead_lettered_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM webhook_endpoint e WHERE e.id = d.endpoint_id)`).Scan(&n)
	if err != nil {
		return 0, wrapDB(err, "the orphaned webhook deliveries could not be counted")
	}

	return n, nil
}

// markDelivered closes a delivery the receiver accepted.
func (s *store) markDelivered(ctx context.Context, id string, status int) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE webhook_delivery
		SET delivered_at = now(), attempts = attempts + 1, last_status = $2, last_error = ''
		WHERE id = $1`, id, status)
	if err != nil {
		return wrapDB(err, "the webhook delivery could not be marked delivered")
	}

	return nil
}

// markFailedSQL records an attempt that did not land, and gives up if this was
// the last one allowed.
//
// The decision is taken HERE, in the same statement that counts the attempt,
// and that is what makes it correct under concurrency: two passes that somehow
// both counted this row cannot disagree about whether it crossed the ceiling,
// because neither reads the counter into Go to compare it. The comparison is
// `attempts + 1` — the value being written — against the ceiling.
//
// dead_lettered_at is set to now() and never cleared here: a row already dead
// keeps the instant it died, so raising the ceiling later cannot rewrite the
// history a human was shown. [store.redrive] is the only way back.
//
// The interval comes from make_interval rather than string concatenation
// because the delay is a parameter, and a delay pasted into SQL text is the one
// place in this file where a value could become syntax.
const markFailedSQL = `
UPDATE webhook_delivery
SET attempts = attempts + 1,
    last_error = $2,
    last_status = $3,
    next_attempt_at = now() + make_interval(secs => $4),
    dead_lettered_at = CASE
        WHEN attempts + 1 >= $5 THEN now()
        ELSE dead_lettered_at
    END
WHERE id = $1
RETURNING dead_lettered_at IS NOT NULL`

// markFailed records a failed attempt and reports whether it was the last.
func (s *store) markFailed(
	ctx context.Context, id, message string, status int, delay time.Duration,
) (bool, error) {
	var dead bool
	err := s.pool.QueryRow(ctx, markFailedSQL,
		id, truncateError(message), status, delay.Seconds(), maxAttempts).Scan(&dead)
	if err != nil {
		return false, wrapDB(err, "the failed webhook attempt could not be recorded")
	}

	return dead, nil
}

// selectDeadLettersSQL reads the pile and measures it in one pass.
//
// count(*) OVER () is evaluated before LIMIT, so it counts the whole filtered
// set rather than the page. Two round trips would have been the obvious
// alternative and they can DISAGREE: a pass dead-lettering rows between the
// count and the page would report a number no sample explains, once a minute,
// with nothing saying the two came from different instants.
const selectDeadLettersSQL = `
SELECT id, endpoint_id, url, event_name, event_id, attempts, last_error, last_status,
       created_at, dead_lettered_at, count(*) OVER () AS total
FROM webhook_delivery
WHERE dead_lettered_at IS NOT NULL
ORDER BY dead_lettered_at, id
LIMIT $1`

// deadLetters reads the pile a human has to look at.
func (s *store) deadLetters(ctx context.Context, limit int32) (deadLetterReport, error) {
	rows, err := s.pool.Query(ctx, selectDeadLettersSQL, limit)
	if err != nil {
		return deadLetterReport{}, wrapDB(err, "the webhook dead letters could not be read")
	}
	defer rows.Close()

	var report deadLetterReport
	for rows.Next() {
		var d deadLetter
		if err := rows.Scan(&d.ID, &d.EndpointID, &d.URL, &d.EventName, &d.EventID,
			&d.Attempts, &d.LastError, &d.LastStatus, &d.CreatedAt, &d.DeadAt,
			&report.Count); err != nil {
			return deadLetterReport{}, wrapDB(err, "a webhook dead letter could not be read")
		}
		report.Oldest = append(report.Oldest, d)
	}

	return report, wrapDB(rows.Err(), "the webhook dead letters could not be read")
}

// redrive puts one dead delivery back in the queue.
//
// attempts is RESET rather than kept, and that is the whole meaning of the
// verb. An operator redrives after fixing the cause; a row that kept its count
// would be back on the pile after one more failure, which is a single retry
// wearing the name of a second chance.
//
// The `dead_lettered_at IS NOT NULL` predicate is not redundant with the id. It
// is what stops this being a way to reset the attempt count of a LIVE row: a
// healthy delivery mid-backoff would jump the queue and lose its history to a
// typo in an id.
func (s *store) redrive(ctx context.Context, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE webhook_delivery
		SET dead_lettered_at = NULL, attempts = 0, next_attempt_at = now(), last_error = ''
		WHERE id = $1 AND dead_lettered_at IS NOT NULL`, id)
	if err != nil {
		return false, wrapDB(err, "the webhook delivery could not be redriven")
	}

	return tag.RowsAffected() > 0, nil
}

// discard removes one dead delivery for good.
//
// It is the other exit, and it deletes rather than marking: a discarded row
// that stayed would need a fourth state, and the three the timestamps already
// carry are exactly the ones a reader can name. What is lost is the record that
// the event was owed — which is why the listing must be read before discarding,
// and why the endpoint's own deletion does not cascade into these rows.
//
// Like [store.redrive] it acts only on a row that is already dead. Discarding a
// live delivery would be a way to silently drop an event nobody has given up on
// yet.
func (s *store) discard(ctx context.Context, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM webhook_delivery WHERE id = $1 AND dead_lettered_at IS NOT NULL`, id)
	if err != nil {
		return false, wrapDB(err, "the webhook delivery could not be discarded")
	}

	return tag.RowsAffected() > 0, nil
}

// pending reads the deliveries that are still owed, oldest first.
//
// It is the listing's other half. A dead letter answers "what did we give up
// on"; this answers "what is stuck right now", and during an outage the second
// is the question an operator arrives with — the pile is still empty then, and
// a surface that could only show the pile would report nothing wrong for the
// first four hours of a receiver being down.
func (s *store) pending(ctx context.Context, limit int32) ([]deadLetter, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, endpoint_id, url, event_name, event_id, attempts, last_error, last_status,
		       created_at, next_attempt_at
		FROM webhook_delivery
		WHERE delivered_at IS NULL AND dead_lettered_at IS NULL
		ORDER BY created_at, id
		LIMIT $1`, limit)
	if err != nil {
		return nil, wrapDB(err, "the pending webhook deliveries could not be read")
	}
	defer rows.Close()

	var out []deadLetter
	for rows.Next() {
		var d deadLetter
		if err := rows.Scan(&d.ID, &d.EndpointID, &d.URL, &d.EventName, &d.EventID,
			&d.Attempts, &d.LastError, &d.LastStatus, &d.CreatedAt, &d.DeadAt); err != nil {
			return nil, wrapDB(err, "a pending webhook delivery could not be read")
		}
		out = append(out, d)
	}

	return out, wrapDB(rows.Err(), "the pending webhook deliveries could not be read")
}

// maxStoredError bounds what a receiver's answer can write into a row.
//
// A body is attacker-influenced text going into a column an admin listing
// prints. Two hundred characters is enough to carry "connection refused" or the
// first line of an HTML error page, and short enough that a receiver answering
// a megabyte of HTML cannot fill the table through the error column.
const maxStoredError = 200

// truncateError bounds and tidies what goes into last_error.
func truncateError(message string) string {
	message = strings.TrimSpace(strings.ReplaceAll(message, "\n", " "))
	if len(message) <= maxStoredError {
		return message
	}

	return message[:maxStoredError] + "…"
}

// newID produces a prefixed identifier.
func newID(prefix string) (string, error) {
	raw := make([]byte, idBodyLength)
	if _, err := rand.Read(raw); err != nil {
		return "", coreerrors.Wrap(err, coreerrors.KindInternal, codeStoreFailed,
			"an identifier could not be produced")
	}

	var b strings.Builder
	b.Grow(len(prefix) + idBodyLength)
	b.WriteString(prefix)
	for _, v := range raw {
		b.WriteByte(idAlphabet[int(v)%len(idAlphabet)])
	}

	return b.String(), nil
}

// newSecret mints an endpoint's signing key.
//
// base64url without padding, so the value survives a .env file, a Kubernetes
// secret and a copy-paste — the same reasoning webpush's VAPID key records for
// preferring a single line over PEM.
func newSecret() (string, error) {
	raw := make([]byte, secretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", coreerrors.Wrap(err, coreerrors.KindInternal, codeStoreFailed,
			"a webhook secret could not be produced")
	}

	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// wrapDB turns a driver error into a classified one.
//
// A canceled context is Unavailable rather than Internal, and the difference is
// not cosmetic: a delivery pass whose deadline elapsed would otherwise be
// reported to the error reporter as a fault in the plugin rather than as the
// budget doing its job.
func wrapDB(err error, message string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeStoreFailed, "%s", message)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return coreerrors.Wrap(err, coreerrors.KindNotFound, codeNotFound, "%s", message)
	}

	return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeStoreFailed, "%s", message)
}
