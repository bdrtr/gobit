-- tax_rate queries. Every read filters on deleted_at IS NULL.

-- name: InsertTaxRate :one
INSERT INTO tax_rate (
    id, tax_region_id, name, code, rate_bps, is_default, metadata,
    stacks_on_id, compound, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
RETURNING *;

-- ListTaxRatesStandingOn returns the rates that stand DIRECTLY on the given
-- ones, in one query.
--
-- It is what expands a chosen rate into its stack: the walk is outward, one
-- level per round trip, and a level holds at most one rate per base
-- (tax_rate_stacks_on_uniq), so the number of round trips is the stack's DEPTH
-- and never the number of rates.
-- name: ListTaxRatesStandingOn :many
SELECT * FROM tax_rate
WHERE stacks_on_id = ANY(@base_ids::text[]) AND deleted_at IS NULL
ORDER BY stacks_on_id, id;

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

-- ListTaxRatesByRegions fetches the rates of ALL the regions in the calculation
-- chain in a single query.
--
-- The batched read keeps the number of queries constant even if the number of
-- regions (at most two) changes; a separate query per region would tie the cost
-- of a calculation to the depth of the hierarchy.
-- name: ListTaxRatesByRegions :many
SELECT * FROM tax_rate
WHERE tax_region_id = ANY(@region_ids::text[]) AND deleted_at IS NULL
ORDER BY tax_region_id, is_default DESC, id;

-- name: CountTaxRatesByRegion :one
SELECT count(*) FROM tax_rate
WHERE tax_region_id = $1 AND deleted_at IS NULL;

-- name: UpdateTaxRate :one
UPDATE tax_rate
SET name = $2, code = $3, rate_bps = $4, is_default = $5, metadata = $6,
    stacks_on_id = $7, compound = $8, updated_at = $9
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
