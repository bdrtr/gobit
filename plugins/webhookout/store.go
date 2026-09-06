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

// endpoint is one registered receiver.
type endpoint struct {
	ID          string
	URL         string
	Secret      string
	Topics      []string
	Description string
	CreatedAt   time.Time
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
	ctx context.Context, url string, topics []string, description string,
) (endpoint, error) {
	id, err := newID(endpointPrefix)
	if err != nil {
		return endpoint{}, err
	}

	secret, err := newSecret()
	if err != nil {
		return endpoint{}, err
	}

	row := endpoint{
		ID: id, URL: url, Secret: secret, Topics: topics, Description: description,
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO webhook_endpoint (id, url, secret, topics, description)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at`,
		id, url, secret, topics, description).Scan(&row.CreatedAt)
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
		SELECT id, url, topics, description, created_at
		FROM webhook_endpoint
		ORDER BY created_at, id`)
	if err != nil {
		return nil, wrapDB(err, "the webhook endpoints could not be listed")
	}
	defer rows.Close()

	var out []endpoint
	for rows.Next() {
		var e endpoint
		if err := rows.Scan(&e.ID, &e.URL, &e.Topics, &e.Description, &e.CreatedAt); err != nil {
			return nil, wrapDB(err, "a webhook endpoint row could not be read")
		}
		out = append(out, e)
	}

	return out, wrapDB(rows.Err(), "the webhook endpoint rows could not be read")
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
const enqueueSQL = `
INSERT INTO webhook_delivery (
    id, endpoint_id, url, event_id, event_name, occurred_at, payload, redacted
)
SELECT $1 || upper(translate(substr(gen_random_uuid()::text, 1, 26), '-', '0')),
       e.id, e.url, $2, $3, $4, $5, $6
FROM webhook_endpoint e
WHERE e.topics @> ARRAY[$3]::text[]
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
// [claimLease], which makes the rows invisible to another pass, and then the
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
// SKIP LOCKED stays because the claim itself must not serialize behind another
// instance's claim; the advisory lock the scheduler takes already makes two
// concurrent passes unlikely, and "unlikely" is not "impossible" (a process
// partitioned from the database after taking the lock is the documented case).
const claimSQL = `
UPDATE webhook_delivery d
SET next_attempt_at = now() + make_interval(secs => $2)
FROM (
    SELECT id
    FROM webhook_delivery
    WHERE delivered_at IS NULL
      AND dead_lettered_at IS NULL
      AND next_attempt_at <= now()
    ORDER BY created_at, id
    LIMIT $1
    FOR UPDATE SKIP LOCKED
) due
JOIN webhook_endpoint e ON TRUE
WHERE d.id = due.id AND e.id = d.endpoint_id
RETURNING d.id, d.endpoint_id, d.url, e.secret, d.event_id, d.event_name,
          d.occurred_at, d.payload, d.redacted, d.attempts`

// claimDue leases up to limit due deliveries and returns them.
//
// A delivery whose endpoint row is GONE is not returned, because the join finds
// no secret to sign with. It is not an oversight and it is not silent: such a
// row can never be sent again, it keeps its place in the due index, and
// [store.orphanCount] is what reports it — the delivery job says so on every
// pass rather than leaving a row that is due forever and never picked up.
func (s *store) claimDue(ctx context.Context, limit int32) ([]delivery, error) {
	rows, err := s.pool.Query(ctx, claimSQL, limit, claimLease.Seconds())
	if err != nil {
		return nil, wrapDB(err, "the due webhook deliveries could not be claimed")
	}
	defer rows.Close()

	var out []delivery
	for rows.Next() {
		var d delivery
		if err := rows.Scan(&d.ID, &d.EndpointID, &d.URL, &d.Secret, &d.EventID,
			&d.EventName, &d.OccurredAt, &d.Payload, &d.Redacted, &d.Attempts); err != nil {
			return nil, wrapDB(err, "a webhook delivery row could not be read")
		}
		out = append(out, d)
	}

	return out, wrapDB(rows.Err(), "the webhook delivery rows could not be read")
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
