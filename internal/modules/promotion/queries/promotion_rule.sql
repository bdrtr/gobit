-- promotion_rule sorguları.

-- name: InsertPromotionRule :one
INSERT INTO promotion_rule (
    id, promotion_id, rule_type, attribute, operator, rule_values,
    created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
RETURNING *;

-- name: GetPromotionRule :one
SELECT * FROM promotion_rule
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListPromotionRules :many
SELECT * FROM promotion_rule
WHERE promotion_id = $1 AND deleted_at IS NULL
ORDER BY id;

-- ListPromotionRulesByPromotions hesaplamaya giren TÜM promosyonların
-- kurallarını tek turda döner; promosyon başına sorgu (N+1) yapılmaz.
-- name: ListPromotionRulesByPromotions :many
SELECT * FROM promotion_rule
WHERE promotion_id = ANY (@promotion_ids::text[]) AND deleted_at IS NULL
ORDER BY promotion_id, id;

-- name: SoftDeletePromotionRule :one
UPDATE promotion_rule
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;
