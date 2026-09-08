-- payment_collections queries.
--
-- The collection row is the FIRST step of the lock order for ALL of a payment's
-- child records (see service.Store, "Transaction boundary"). Every flow that
-- writes a session, a capture or a refund begins its transaction with
-- LockPaymentCollection; that is how two flows touching the same collection
-- serialize against each other, and it is why the derived status field is never
-- written from two different computations.

-- name: CreatePaymentCollection :one
INSERT INTO payment_collections (
    id, reference, amount, currency_code, status, metadata
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetPaymentCollection :one
SELECT * FROM payment_collections
WHERE id = $1;

-- LockPaymentCollection locks the collection for the length of the transaction
-- and returns its current form. Every flow that changes the amounts does its
-- reading with THIS method: an amount read without the lock can be stale by the
-- moment it is written, and two concurrent captures could spend the same
-- authorization twice.
-- name: LockPaymentCollection :one
SELECT * FROM payment_collections
WHERE id = $1
FOR UPDATE;

-- name: ListPaymentCollections :many
SELECT * FROM payment_collections
WHERE (sqlc.narg('reference')::text IS NULL OR reference = sqlc.narg('reference')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountPaymentCollections gives the total count for the pagination envelope and
-- applies the SAME filters as ListPaymentCollections; the two have to be
-- changed together.
--
-- The total cannot be read from a window function returned alongside the rows:
-- on a page past the end no row comes back at all, the window is never
-- evaluated, and the total would look like 0. The total is the count of the
-- FILTER, not of the page.
-- name: CountPaymentCollections :one
SELECT COUNT(*) FROM payment_collections
WHERE (sqlc.narg('reference')::text IS NULL OR reference = sqlc.narg('reference')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text);

-- GetPaymentCollectionsByIDs answers the Query layer's FetchByIDs call in ONE
-- round trip; no query is made per identifier (N+1).
-- name: GetPaymentCollectionsByIDs :many
SELECT * FROM payment_collections
WHERE id = ANY (sqlc.arg('ids')::text[])
ORDER BY id;

-- PaymentMomentsByCollectionIDs returns the collections' money MOMENTS in a
-- single query.
--
-- The amounts are already on the collection row; what was missing were the
-- TIMES, and both of them live in other tables: when the money first moved
-- (payments.captured_at) and when the last refund went out (refunds.created_at
-- — the refunds table has no separate refunded_at column).
--
-- The first is a MIN, the second a MAX, and the asymmetry is deliberate: when a
-- support desk asks "when was it paid" it means the moment the money BEGAN TO
-- MOVE, and when it asks "when was it refunded" it means the LATEST refund. A
-- partial capture and a partial refund make both of them plural; a read that
-- wants a single moment wants these two, and a read that wants every moment is
-- a separate call.
--
-- There is no THIRD moment beside these two, and its absence is a decision
-- rather than a gap (ADR 0054). Both of these are moments MONEY MOVED. An
-- authorization is a hold and moves none; while the hold is what a reader is
-- asking about, the session is still 'authorized' and its updated_at IS that
-- moment, because every transition out of the status leaves the status and
-- re-authorizing an authorized session is a no-op.
--
-- Both subqueries use the per-collection and per-payment indexes.
-- name: PaymentMomentsByCollectionIDs :many
SELECT c.id AS payment_collection_id,
       (SELECT min(p.captured_at)
          FROM payments p
         WHERE p.payment_collection_id = c.id)::timestamptz AS first_captured_at,
       (SELECT max(r.created_at)
          FROM refunds r
          JOIN payments p2 ON p2.id = r.payment_id
         WHERE p2.payment_collection_id = c.id)::timestamptz AS last_refunded_at
FROM payment_collections c
WHERE c.id = ANY (sqlc.arg('ids')::text[])
ORDER BY c.id;

-- UpdatePaymentCollectionTotals writes the amounts and the derived status as
-- ABSOLUTE values.
--
-- An incremental update (amount = amount + n) is deliberately not used: the new
-- value is computed from the value read under the lock, so the number the code
-- that made the decision saw and the number that gets written are the same one.
-- name: UpdatePaymentCollectionTotals :one
UPDATE payment_collections
SET status            = $2,
    authorized_amount = $3,
    captured_amount   = $4,
    refunded_amount   = $5,
    updated_at        = now()
WHERE id = $1
RETURNING *;
