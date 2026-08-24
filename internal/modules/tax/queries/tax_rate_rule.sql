-- tax_rate_rule sorguları. Tüm okumalar deleted_at IS NULL süzer.

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

-- ListTaxRateRulesByRates hesaba giren TÜM oranların kurallarını tek sorguda
-- getirir; kural sayısı ne olursa olsun gidiş dönüş sayısı sabittir (N+1 yok).
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
