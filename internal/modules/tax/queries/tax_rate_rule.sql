-- tax_rate_rule queries. Every read filters on deleted_at IS NULL.

-- name: InsertTaxRateRule :one
INSERT INTO tax_rate_rule (id, tax_rate_id, reference, reference_id, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $5)
RETURNING *;

-- name: GetTaxRateRule :one
SELECT * FROM tax_rate_rule
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListTaxRateRulesByRate :many
SELECT * FROM tax_rate_rule
WHERE tax_rate_id = $1 AND deleted_at IS NULL
ORDER BY id;

-- ListTaxRateRulesByRates fetches the rules of ALL the rates entering the
-- calculation in a single query; however many rules there are, the number of
-- round trips stays constant (no N+1).
-- name: ListTaxRateRulesByRates :many
SELECT * FROM tax_rate_rule
WHERE tax_rate_id = ANY(@rate_ids::text[]) AND deleted_at IS NULL
ORDER BY tax_rate_id, id;

-- name: CountTaxRateRulesByRate :one
SELECT count(*) FROM tax_rate_rule
WHERE tax_rate_id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteTaxRateRule :one
UPDATE tax_rate_rule
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- name: SoftDeleteTaxRateRulesByRates :exec
UPDATE tax_rate_rule
SET deleted_at = @deleted_at, updated_at = @deleted_at
WHERE tax_rate_id = ANY(@rate_ids::text[]) AND deleted_at IS NULL;
