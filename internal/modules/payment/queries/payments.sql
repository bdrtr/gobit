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
