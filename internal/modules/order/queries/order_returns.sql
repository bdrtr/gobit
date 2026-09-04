-- order_returns queries (return skeleton; plan Section 6).
--
-- Phase 6 offers only creation and listing; the return workflow belongs to the
-- later phases.

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
