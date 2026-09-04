-- order_claims queries (damage/shortage record skeleton; plan Section 6).

-- name: CreateOrderClaim :one
INSERT INTO order_claims (id, order_id, claim_type, status, refund_amount, reason, note, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetOrderClaim :one
SELECT * FROM order_claims
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListOrderClaims :many
SELECT * FROM order_claims
WHERE order_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- name: CountOrderClaims :one
SELECT COUNT(*) FROM order_claims
WHERE order_id = $1 AND deleted_at IS NULL;
