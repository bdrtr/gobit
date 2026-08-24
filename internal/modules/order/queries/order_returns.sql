-- order_returns sorguları (iade iskeleti; plan Bölüm 6).
--
-- Faz 6 yalnızca oluşturma ve listeleme sunar; iade iş akışı sonraki fazlara
-- aittir.

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
