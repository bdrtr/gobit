-- order_returns queries (plan Section 6).
--
-- The record is created, read, listed and MOVED. What is still not here is
-- acting on it: taking the stock back and refunding the payment reach across
-- modules and belong to a flow.

-- name: CreateOrderReturn :one
INSERT INTO order_returns (id, order_id, status, refund_amount, reason, note, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetOrderReturn :one
SELECT * FROM order_returns
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListOrderReturns :many
SELECT * FROM order_returns
WHERE order_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- name: CountOrderReturns :one
SELECT COUNT(*) FROM order_returns
WHERE order_id = $1 AND deleted_at IS NULL;

-- LockOrderReturn locks the return row until the end of the transaction.
--
-- Every transition reads the current status and writes the next one, so the two
-- have to happen without anybody stepping in between: two operators clicking
-- "received" at the same moment would otherwise both read "requested" and both
-- write a timestamp, and the record would keep the later one.
-- name: LockOrderReturn :one
SELECT * FROM order_returns
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- ReceiveOrderReturn stamps the moment the goods came back.
--
-- received_at is written from the DATABASE clock rather than from the caller:
-- the moment belongs to the record, and letting an application supply it makes
-- the ordering of two records depend on which machine wrote them.
-- name: ReceiveOrderReturn :one
UPDATE order_returns
SET status = 'received', received_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- CancelOrderReturn withdraws the request.
-- name: CancelOrderReturn :one
UPDATE order_returns
SET status = 'canceled', canceled_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;
