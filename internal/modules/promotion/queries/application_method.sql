-- promotion_application_method queries.

-- UpsertApplicationMethod writes the method; if the promotion already has one
-- it OVERWRITES it.
--
-- The upsert is deliberate: there is at most one method per promotion, and a
-- "delete first, then insert" would leave the promotion without a method
-- between the two statements — a computation running in that gap would produce
-- no discount. The conflict target is the partial unique index; a deleted
-- method does not take part in the conflict, which is why the WHERE condition
-- here is character for character the index's own.
-- name: UpsertApplicationMethod :one
INSERT INTO promotion_application_method (
    id, promotion_id, type, target_type, allocation, value, max_quantity,
    buy_quantity, apply_to_quantity, currency_code, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
ON CONFLICT (promotion_id) WHERE deleted_at IS NULL
DO UPDATE SET
    type              = EXCLUDED.type,
    target_type       = EXCLUDED.target_type,
    allocation        = EXCLUDED.allocation,
    value             = EXCLUDED.value,
    max_quantity      = EXCLUDED.max_quantity,
    buy_quantity      = EXCLUDED.buy_quantity,
    apply_to_quantity = EXCLUDED.apply_to_quantity,
    currency_code     = EXCLUDED.currency_code,
    updated_at        = EXCLUDED.updated_at
RETURNING *;

-- name: GetApplicationMethod :one
SELECT * FROM promotion_application_method
WHERE promotion_id = $1 AND deleted_at IS NULL;

-- GetApplicationMethodsByPromotions returns the methods of ALL the promotions
-- entering the computation in one round trip; no query is issued per promotion
-- (N+1).
-- name: GetApplicationMethodsByPromotions :many
SELECT * FROM promotion_application_method
WHERE promotion_id = ANY (@promotion_ids::text[]) AND deleted_at IS NULL
ORDER BY promotion_id;

-- name: SoftDeleteApplicationMethod :one
UPDATE promotion_application_method
SET deleted_at = $2, updated_at = $2
WHERE promotion_id = $1 AND deleted_at IS NULL
RETURNING id;
