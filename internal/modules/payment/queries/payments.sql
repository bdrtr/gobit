-- payments queries.
--
-- AT MOST ONE capture comes out of a session (payments_session_uniq). That is
-- what makes Capture idempotent: the second call writes no new row, it finds
-- the existing one with GetPaymentBySession and returns it.

-- name: CreatePayment :one
INSERT INTO payments (
    id, payment_session_id, payment_collection_id, amount, currency_code, captured_at
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetPayment :one
SELECT * FROM payments
WHERE id = $1;

-- LockPayment locks the capture for the length of the transaction; the refunded
-- amount is updated only under this lock. In the lock order it comes AFTER the
-- collection and the session (see service.Store, "Transaction boundary").
-- name: LockPayment :one
SELECT * FROM payments
WHERE id = $1
FOR UPDATE;

-- name: GetPaymentBySession :one
SELECT * FROM payments
WHERE payment_session_id = $1;

-- name: ListPaymentsByCollection :many
SELECT * FROM payments
WHERE payment_collection_id = $1
ORDER BY created_at DESC, id DESC;

-- name: UpdatePaymentRefundedAmount :one
UPDATE payments
SET refunded_amount = $2,
    updated_at      = now()
WHERE id = $1
RETURNING *;

-- CollectionNetCapturedExcludingProvider is the money a collection has captured
-- and not refunded through every tender BUT one.
--
-- It is the earn path's base (ADR 0165): a capture paid with loyalty points earns
-- nothing, or a point would earn itself back at the ceiling rate. The collection
-- row's own totals are provider-blind, so the base is taken one table down, from
-- the captures, whose session names the provider. It is read under the
-- collection's lock, after the capture or the refund it is earning for has been
-- written; payments.refunded_amount is that capture's own running refund sum.
--
-- COALESCE because a collection with no captures has earned zero, which is a
-- number rather than an absence.
-- name: CollectionNetCapturedExcludingProvider :one
SELECT COALESCE(SUM(p.amount - p.refunded_amount), 0)::bigint AS net
FROM payments p
JOIN payment_sessions s ON s.id = p.payment_session_id
WHERE p.payment_collection_id = sqlc.arg('payment_collection_id')
  AND s.provider_id <> sqlc.arg('excluded_provider_id');
