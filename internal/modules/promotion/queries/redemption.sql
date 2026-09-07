-- promotion_redemption queries.

-- name: InsertRedemption :one
INSERT INTO promotion_redemption (
    id, promotion_id, campaign_id, reference, amount, currency_code,
    budget_delta, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING *;

-- GetActiveRedemption returns the VALID (not yet released) redemption for a
-- reference.
--
-- It is the read side of idempotency: a RedeemPromotion called a second time
-- with the same reference returns this record instead of raising the counter.
-- name: GetActiveRedemption :one
SELECT * FROM promotion_redemption
WHERE promotion_id = $1 AND reference = $2 AND released_at IS NULL;

-- LockActiveRedemption locks the VALID redemption for the duration of the
-- transaction.
--
-- The release takes this lock; of two concurrent Releases only one can write
-- the row as "released", and the other sees the updated row and does nothing.
-- That is what stops the counter being lowered twice.
-- name: LockActiveRedemption :one
SELECT * FROM promotion_redemption
WHERE promotion_id = $1 AND reference = $2 AND released_at IS NULL
FOR UPDATE;

-- name: ListRedemptions :many
SELECT * FROM promotion_redemption
WHERE promotion_id = $1
ORDER BY id
LIMIT $2 OFFSET $3;

-- name: CountRedemptions :one
SELECT count(*) FROM promotion_redemption
WHERE promotion_id = $1;

-- MarkRedemptionReleased marks the redemption as released.
--
-- The CONDITION is released_at IS NULL: an already released record returns no
-- row at all and the caller does not perform the second decrement. That is the
-- second defence keeping the compensation idempotent (the first is the row
-- lock).
-- name: MarkRedemptionReleased :one
UPDATE promotion_redemption
SET released_at = $2, updated_at = $2
WHERE id = $1 AND released_at IS NULL
RETURNING *;
