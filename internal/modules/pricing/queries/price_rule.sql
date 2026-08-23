-- price_rule sorguları.

-- name: InsertPriceRule :one
INSERT INTO price_rule (id, price_id, attribute, operator, rule_values, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $6)
RETURNING *;

-- name: GetPriceRule :one
SELECT * FROM price_rule
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListPriceRulesByPrice :many
SELECT * FROM price_rule
WHERE price_id = $1 AND deleted_at IS NULL
ORDER BY id;

-- name: ListPriceRulesByPrices :many
SELECT * FROM price_rule
WHERE price_id = ANY(@price_ids::text[]) AND deleted_at IS NULL
ORDER BY price_id, id;

-- name: SoftDeletePriceRule :one
UPDATE price_rule
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;
