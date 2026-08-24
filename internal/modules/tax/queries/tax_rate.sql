-- tax_rate sorguları. Tüm okumalar deleted_at IS NULL süzer.

-- name: InsertTaxRate :one
INSERT INTO tax_rate (id, tax_region_id, name, code, rate_bps, is_default, metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING *;

-- name: GetTaxRate :one
SELECT * FROM tax_rate
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetTaxRateForUpdate :one
SELECT * FROM tax_rate
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: ListTaxRatesByRegion :many
SELECT * FROM tax_rate
WHERE tax_region_id = $1 AND deleted_at IS NULL
ORDER BY is_default DESC, id;

-- ListTaxRatesByRegions hesap zincirindeki TÜM bölgelerin oranlarını tek
-- sorguda getirir.
--
-- Toplu okuma, bölge sayısı (en fazla iki) değişse bile sorgu sayısını sabit
-- tutar; bölge başına ayrı sorgu, hesabın maliyetini hiyerarşinin derinliğine
-- bağlardı.
-- name: ListTaxRatesByRegions :many
SELECT * FROM tax_rate
WHERE tax_region_id = ANY(@region_ids::text[]) AND deleted_at IS NULL
ORDER BY tax_region_id, is_default DESC, id;

-- name: CountTaxRatesByRegion :one
SELECT count(*) FROM tax_rate
WHERE tax_region_id = $1 AND deleted_at IS NULL;

-- name: UpdateTaxRate :one
UPDATE tax_rate
SET name = $2, code = $3, rate_bps = $4, is_default = $5, metadata = $6, updated_at = $7
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteTaxRate :one
UPDATE tax_rate
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- name: SoftDeleteTaxRatesByRegions :many
UPDATE tax_rate
SET deleted_at = @deleted_at, updated_at = @deleted_at
WHERE tax_region_id = ANY(@region_ids::text[]) AND deleted_at IS NULL
RETURNING id;
