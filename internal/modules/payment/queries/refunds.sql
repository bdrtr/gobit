-- refunds queries.
--
-- A partial refund produces several rows; their sum is kept in the
-- payments.refunded_amount column, and the two values are written in the same
-- transaction, under the capture's lock.

-- name: CreateRefund :one
INSERT INTO refunds (
    id, payment_id, amount, reason
) VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetRefund :one
SELECT * FROM refunds
WHERE id = $1;

-- name: ListRefundsByPayment :many
SELECT * FROM refunds
WHERE payment_id = $1
ORDER BY created_at DESC, id DESC;
