-- order_claims queries (damage/shortage record skeleton; plan Section 6).

-- name: CreateOrderClaim :one
INSERT INTO order_claims (id, order_id, claim_type, status, refund_amount, reason, note, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetOrderClaim :one
SELECT * FROM order_claims
WHERE id = $1;

-- name: ListOrderClaims :many
SELECT * FROM order_claims
WHERE order_id = $1
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- name: CountOrderClaims :one
SELECT COUNT(*) FROM order_claims
WHERE order_id = $1;

-- LockOrderClaim locks the claim row until the end of the transaction.
-- name: LockOrderClaim :one
SELECT * FROM order_claims
WHERE id = $1
FOR UPDATE;

-- CompleteOrderClaim records that the claim was settled.
-- name: CompleteOrderClaim :one
UPDATE order_claims
SET status = 'completed', completed_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- ReopenOrderClaim takes a completed claim back to 'requested' when the goods
-- that completed it did not leave: their parcel was canceled (ADR 0239).
-- name: ReopenOrderClaim :one
UPDATE order_claims
SET status = 'requested', completed_at = NULL, updated_at = now()
WHERE id = $1 AND status = 'completed'
RETURNING *;

-- CancelOrderClaim withdraws the claim.
-- name: CancelOrderClaim :one
UPDATE order_claims
SET status = 'canceled', canceled_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- ListOrderClaimsByIDs reads the given claims for the read layer's batch path
-- (ADR 0270), newest first like the order's own listing.
-- name: ListOrderClaimsByIDs :many
SELECT * FROM order_claims
WHERE id = ANY (sqlc.arg('ids')::text[])
ORDER BY created_at DESC, id DESC;
