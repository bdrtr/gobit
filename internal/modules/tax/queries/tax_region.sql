-- tax_region queries. Every read filters on deleted_at IS NULL.

-- name: InsertTaxRegion :one
INSERT INTO tax_region (id, country_code, province_code, parent_id, provider_id, prices_include_tax, metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING *;

-- name: GetTaxRegion :one
SELECT * FROM tax_region
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetTaxRegionsByIDs :many
SELECT * FROM tax_region
WHERE id = ANY(@ids::text[]) AND deleted_at IS NULL
ORDER BY id;

-- name: ListTaxRegions :many
SELECT * FROM tax_region
WHERE deleted_at IS NULL
  AND (@country_code::text = '' OR country_code = @country_code::text)
ORDER BY country_code, (parent_id IS NULL) DESC, id
LIMIT $1 OFFSET $2;

-- name: CountTaxRegions :one
SELECT count(*) FROM tax_region
WHERE deleted_at IS NULL
  AND (@country_code::text = '' OR country_code = @country_code::text);

-- ResolveTaxRegions returns a country's root and (when one is given) its
-- province region in a SINGLE query.
--
-- Being one query is deliberate: the calculation path is called on every cart
-- round, and two round trips would double the cost of resolving the region.
-- When the province code is given empty, the second condition matches no row
-- (province_code cannot be empty, its CHECK forbids it), so only the root comes
-- back.
--
-- The order is PROVINCE FIRST: the calculation chain walks from the MOST
-- SPECIFIC to the general, and fixing that order in the query is what spares
-- the service from having to reorder the rows.
-- name: ResolveTaxRegions :many
SELECT * FROM tax_region
WHERE deleted_at IS NULL
  AND country_code = @country_code::text
  AND (parent_id IS NULL OR province_code = @province_code::text)
ORDER BY (parent_id IS NULL), id;

-- name: GetTaxRegionForUpdate :one
SELECT * FROM tax_region
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- SoftDeleteTaxRegionTree deletes the region and (when it is a root) its
-- sub-regions together.
--
-- Because the tree is two levels deep, the "itself or its child" condition
-- covers the whole subtree; no recursive CTE is needed. The deleted ids are
-- RETURNED: the caller uses them to delete the rates within the same
-- transaction. Were the region deleted and its rates left behind, a new region
-- opened for the same country would not see the old rates, but those old rates
-- would sit in the ledger as orphan rows and would corrupt report totals.
-- name: SoftDeleteTaxRegionTree :many
UPDATE tax_region
SET deleted_at = @deleted_at, updated_at = @deleted_at
WHERE deleted_at IS NULL AND (id = @id::text OR parent_id = @id::text)
RETURNING id;

-- GetTaxRegionForShare reads the region and takes a SHARED lock on its row.
--
-- The lock is there for the flows that ATTACH SOMETHING to a region: adding a
-- province region and adding a rate. Both of them check "is the region alive"
-- and then write; were the check unlocked, a deletion stepping in could
-- complete AFTER the check and BEFORE the write, and the row would end up
-- attached to a DELETED region. A foreign key does not catch this, because soft
-- deletion leaves the row in place: the constraint looks at the row's
-- EXISTENCE, not at its deleted_at.
--
-- The lock is SHARED, not exclusive. There is no reason at all for two
-- concurrent rate insertions on the same region to wait for each other; the one
-- flow that must wait is deletion, and that one takes an EXCLUSIVE lock with
-- GetTaxRegionForUpdate — FOR SHARE conflicts with FOR UPDATE, two FOR SHAREs
-- do not conflict.
--
-- The WHERE condition is re-evaluated AFTER the lock is acquired: when the
-- waiting request wakes up it sees the CURRENT form of the row and returns "not
-- found" if it has been deleted.
-- name: GetTaxRegionForShare :one
SELECT * FROM tax_region
WHERE id = $1 AND deleted_at IS NULL
FOR SHARE;
