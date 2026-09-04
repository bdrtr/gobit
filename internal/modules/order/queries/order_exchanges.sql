-- order_exchanges queries (exchange skeleton; plan Section 6).

-- name: CreateOrderExchange :one
INSERT INTO order_exchanges (id, order_id, status, difference_due, note, metadata)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetOrderExchange :one
SELECT * FROM order_exchanges
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListOrderExchanges :many
SELECT * FROM order_exchanges
WHERE order_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- name: CountOrderExchanges :one
SELECT COUNT(*) FROM order_exchanges
WHERE order_id = $1 AND deleted_at IS NULL;
