-- refunds queries.
--
-- A partial refund produces several rows; their sum is kept in the
-- payments.refunded_amount column, and the two values are written in the same
-- transaction, under the capture's lock.

-- name: CreateRefund :one
INSERT INTO refunds (
    id, payment_id, amount, reason, reference
) VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetRefund :one
SELECT * FROM refunds
WHERE id = $1;

-- name: ListRefundsByPayment :many
SELECT * FROM refunds
WHERE payment_id = $1
ORDER BY created_at DESC, id DESC;

-- What the refunds naming one cause gave back, in every collection (ADR 0433):
-- the returns flow holds a cause's refunds to a ceiling, and the sum is read
-- again under the collection's lock before a refund row is written. The second
-- predicate lets the planner use refunds_reference_idx, which leaves out the
-- refunds that name no cause.
-- name: RefundedForReference :one
SELECT COALESCE(SUM(amount), 0)::bigint AS refunded FROM refunds
WHERE reference = $1 AND reference <> '';
