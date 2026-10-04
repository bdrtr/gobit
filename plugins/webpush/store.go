package webpush

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// idPrefix marks a subscription id.
//
// The prefix is the repository's convention and it earns its place here twice
// over: the id appears in an admin URL, and an operator reading a log line has
// to be able to tell a device row from the customer id sitting next to it.
const idPrefix = "wps_"

// idBodyLength is the number of random characters after the prefix.
const idBodyLength = 26

// idAlphabet is Crockford Base32 without the letters that misread.
const idAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Error codes.
const (
	codeStoreFailed = "webpush_store_failed"
	codeNotFound    = "webpush_subscription_not_found"
)

// subscription is one device.
type subscription struct {
	ID          string
	Endpoint    string
	P256DH      string
	Auth        string
	CustomerID  string
	Locale      string
	Fingerprint string
}

// store is the plugin's data access. It owns two tables: the device registry and
// the record of the events it pushed (ADR 0389).
type store struct {
	pool *pgxpool.Pool
}

// newStore builds the store over the core pool.
func newStore(pool *pgxpool.Pool) *store { return &store{pool: pool} }

// upsertSQL writes a device, or updates the one already holding its endpoint.
//
// # What is overwritten and what is not
//
// p256dh and auth are overwritten UNCONDITIONALLY: they are what the browser
// just minted, and a stale pair means encrypting messages that device can no
// longer open — a failure with no error on either side.
//
// customer_id and locale are never downgraded to empty. A returning browser
// re-subscribes without sending a customer id, and taking that as "log this
// device out" would silently unbind every device on every refresh. The cost of
// that rule is that nothing can clear the binding any more, which is why
// [store.unbind] exists.
const upsertSQL = `
INSERT INTO webpush_subscription (
    id, endpoint, p256dh, auth, customer_id, locale, vapid_fingerprint
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (endpoint) DO UPDATE SET
    p256dh            = EXCLUDED.p256dh,
    auth              = EXCLUDED.auth,
    customer_id       = CASE WHEN EXCLUDED.customer_id <> '' THEN EXCLUDED.customer_id
                             ELSE webpush_subscription.customer_id END,
    locale            = CASE WHEN EXCLUDED.locale <> '' THEN EXCLUDED.locale
                             ELSE webpush_subscription.locale END,
    vapid_fingerprint = EXCLUDED.vapid_fingerprint,
    updated_at        = now()
RETURNING id`

// upsert stores a device and returns its row id.
func (s *store) upsert(ctx context.Context, sub subscription) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}

	var stored string
	err = s.pool.QueryRow(ctx, upsertSQL,
		id, sub.Endpoint, sub.P256DH, sub.Auth, sub.CustomerID, sub.Locale, sub.Fingerprint,
	).Scan(&stored)
	if err != nil {
		return "", wrapDB(err, "the subscription could not be stored")
	}

	return stored, nil
}

// deleteByEndpoint removes a device by the endpoint it holds.
//
// It is IDEMPOTENT: an endpoint that is not stored is not an error. The caller
// is an unsubscribe request from a browser that may have been unsubscribed
// already, or a send path acting on a 410 it may have acted on before.
func (s *store) deleteByEndpoint(ctx context.Context, endpoint string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM webpush_subscription WHERE endpoint = $1`, endpoint)
	if err != nil {
		return wrapDB(err, "the subscription could not be deleted")
	}

	return nil
}

// deleteByID removes a device by its row id. Idempotent, for the same reason.
func (s *store) deleteByID(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM webpush_subscription WHERE id = $1`, id)
	if err != nil {
		return wrapDB(err, "the subscription could not be deleted")
	}

	return nil
}

// unbind clears the customer binding but KEEPS the device.
//
// This is what logout calls, and it is the counterpart of the upsert's
// never-downgrade rule. Deleting the row instead would be wrong in a way that
// is easy to miss: the browser's permission grant survives, so the device would
// keep its subscription while the server forgot it, and the next re-subscribe
// would be the only thing that repaired it.
func (s *store) unbind(ctx context.Context, endpoint string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE webpush_subscription SET customer_id = '', updated_at = now() WHERE endpoint = $1`,
		endpoint)
	if err != nil {
		return wrapDB(err, "the subscription could not be unbound")
	}

	return nil
}

// byCustomer lists the devices bound to a customer.
//
// An empty customer id returns NOTHING rather than every unbound device. The
// difference matters: an empty customer_id would match every device that
// subscribed before signing in, so a guest order would push to strangers.
func (s *store) byCustomer(ctx context.Context, customerID string) ([]subscription, error) {
	if strings.TrimSpace(customerID) == "" {
		return nil, nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, endpoint, p256dh, auth, customer_id, locale, vapid_fingerprint
		FROM webpush_subscription
		WHERE customer_id = $1
		ORDER BY created_at`, customerID)
	if err != nil {
		return nil, wrapDB(err, "the customer's devices could not be read")
	}

	return collect(rows)
}

// all lists every device, for the broadcast path and the admin surface.
//
// It pages by a cursor on the primary key rather than OFFSET: a broadcast walks
// the whole table while the send path is deleting dead rows out of it, and
// OFFSET would silently skip rows as the set shifts underneath it.
func (s *store) all(ctx context.Context, after string, limit int) ([]subscription, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, endpoint, p256dh, auth, customer_id, locale, vapid_fingerprint
		FROM webpush_subscription
		WHERE id > $1
		ORDER BY id
		LIMIT $2`, after, limit)
	if err != nil {
		return nil, wrapDB(err, "the devices could not be listed")
	}

	return collect(rows)
}

// countByFingerprint reports how many devices were minted under each signing
// key.
//
// Register logs this. With one entry the installation is healthy; with two the
// VAPID key was rotated and the older group can only ever answer 401 — a fault
// that is otherwise completely invisible, because 401 correctly never deletes.
func (s *store) countByFingerprint(ctx context.Context) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT vapid_fingerprint, count(*) FROM webpush_subscription GROUP BY vapid_fingerprint`)
	if err != nil {
		return nil, wrapDB(err, "the subscriptions could not be counted")
	}
	defer rows.Close()

	counts := map[string]int64{}
	for rows.Next() {
		var fingerprint string
		var n int64
		if err := rows.Scan(&fingerprint, &n); err != nil {
			return nil, wrapDB(err, "the subscription counts could not be read")
		}
		counts[fingerprint] = n
	}

	return counts, rows.Err()
}

// collect reads a result set into subscriptions.
func collect(rows pgx.Rows) ([]subscription, error) {
	defer rows.Close()

	var out []subscription
	for rows.Next() {
		var s subscription
		if err := rows.Scan(&s.ID, &s.Endpoint, &s.P256DH, &s.Auth,
			&s.CustomerID, &s.Locale, &s.Fingerprint); err != nil {
			return nil, wrapDB(err, "a subscription row could not be read")
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDB(err, "the subscription rows could not be read")
	}

	return out, nil
}

// newID produces a subscription id.
func newID() (string, error) {
	raw := make([]byte, idBodyLength)
	if _, err := rand.Read(raw); err != nil {
		return "", coreerrors.Wrap(err, coreerrors.KindInternal, codeStoreFailed,
			"a subscription id could not be produced")
	}

	var b strings.Builder
	b.Grow(len(idPrefix) + idBodyLength)
	b.WriteString(idPrefix)
	for _, v := range raw {
		b.WriteByte(idAlphabet[int(v)%len(idAlphabet)])
	}

	return b.String(), nil
}

// wrapDB turns a driver error into a classified one.
//
// A canceled context is Unavailable rather than Internal, and the difference is
// not cosmetic: a broadcast that the operator's request abandoned would
// otherwise be reported to the error reporter as a fault in the plugin.
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

// pageSize is how many devices a broadcast reads per round.
//
// It bounds memory rather than throughput: a store with a hundred thousand
// devices must not be read into one slice, and the number is small enough that
// a round's worth of rows is negligible next to the HTTP requests they produce.
const pageSize = 500

// deleteByCustomer removes every device bound to one customer and reports how
// many rows went.
//
// It exists for the erasure contract (ADR 0029) and for nothing else. A push
// subscription is a live capability to reach a device rather than a record of
// anything, so the answer to "forget this person" is a DELETE and not a rewrite:
// see the module's Erase for why anonymizing one would be either useless or
// dangerous.
//
// An empty customer id deletes NOTHING. The column defaults to the empty string
// for a device that subscribed before signing in, so treating "" as a handle
// would erase every anonymous subscription in the table on behalf of one person.
func (s *store) deleteByCustomer(ctx context.Context, customerID string) (int64, error) {
	if strings.TrimSpace(customerID) == "" {
		return 0, nil
	}

	tag, err := s.pool.Exec(ctx, `
		DELETE FROM webpush_subscription
		WHERE customer_id = $1`, customerID)
	if err != nil {
		return 0, wrapDB(err, "the customer's devices could not be deleted")
	}

	return tag.RowsAffected(), nil
}

// tableClaimedEvent is the record of the order events the plugin pushed
// (ADR 0389, D236).
const tableClaimedEvent = "webpush_claimed_event"

// claimRetention is how long the record keeps an event's id.
//
// It has to exceed [pushHorizon] plus any skew between the application's clock,
// which judges an order's age, and the database's, which judges a row's: an id
// the record has forgotten must belong to an order the age check already
// refuses, or a late delivery of it pushes again. A day is six times the
// horizon, far more than any skew.
const claimRetention = 24 * time.Hour

// claimSQL records an event's id, and returns it only when this statement wrote
// the row.
//
// A second INSERT of an id another transaction has written but not committed
// waits for that transaction, and then does nothing: the primary key, not a
// read before the write, decides which of two deliveries arriving together is
// the first.
const claimSQL = `
INSERT INTO webpush_claimed_event (event_id)
VALUES ($1)
ON CONFLICT (event_id) DO NOTHING
RETURNING event_id`

// claim records that the plugin is about to push for an event, and reports
// whether this call is the first to do so.
//
// An id already recorded is (false, nil), not an error: a second delivery is
// the ordinary case the record exists for. pgx answers it with ErrNoRows, which
// [wrapDB] would make a NotFound, so it is read before the error is wrapped.
func (s *store) claim(ctx context.Context, eventID string) (bool, error) {
	var recorded string
	err := s.pool.QueryRow(ctx, claimSQL, eventID).Scan(&recorded)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, wrapDB(err, "the pushed event could not be recorded")
	}

	return true, nil
}

// forgetSQL deletes the records older than the retention.
//
// now() is the DATABASE's clock, as the outbox relay's due check uses it
// (core/eventbus/outbox/relay.go): claimed_at is written by the same clock, so
// both sides of the comparison come from one. The retention is bound as seconds
// in a float, since a Go duration is nanoseconds.
const forgetSQL = `
DELETE FROM webpush_claimed_event
WHERE claimed_at < now() - make_interval(secs => $1::float8)`

// forget deletes the records older than retention and reports how many went.
//
// It is a statement of its own rather than part of [claimSQL]: a DELETE and an
// INSERT ... ON CONFLICT on the same key in one statement see one snapshot, and
// which of them wins is not something to depend on.
func (s *store) forget(ctx context.Context, retention time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, forgetSQL, retention.Seconds())
	if err != nil {
		return 0, wrapDB(err, "the old pushed-event records could not be deleted")
	}

	return tag.RowsAffected(), nil
}
