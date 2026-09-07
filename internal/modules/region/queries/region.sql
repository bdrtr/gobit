-- region queries.
--
-- Reads of the region table apply a deleted_at IS NULL filter; a region is
-- SOFT deleted (DeleteRegion). The same does NOT HOLD for country and
-- currency: their columns were dropped in 000003 and the argument is in that
-- file.

-- name: InsertRegion :one
INSERT INTO region (id, name, currency_code, automatic_taxes, tax_rate, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $6)
RETURNING *;

-- name: GetRegion :one
SELECT * FROM region
WHERE id = $1 AND deleted_at IS NULL;

-- GetRegionForUpdate reads the region and locks its row UNTIL THE END OF THE
-- TRANSACTION.
--
-- The partial update (the patch) and the delete are done under this lock:
-- because a patch is written on top of the row it read, two unlocked concurrent
-- updates could each undo the other's field (a lost update). FOR UPDATE
-- re-evaluates the WHERE condition AFTER the lock has been taken; a delete that
-- slipped in between therefore shows up as "no such record".
-- name: GetRegionForUpdate :one
SELECT * FROM region
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- GetRegionForShare reads the region and takes a SHARED lock on it.
--
-- It is the first step IN THE LOCK ORDER of the country-assigning flow: region
-- first, country second. The order is the same in every flow; reversing it
-- means a deadlock.
--
-- The lock is shared: two requests adding different countries to the same
-- region do not wait for each other. It still conflicts with the flows that
-- CHANGE the region (delete, update), which is to say no country can be added
-- to a region that is being deleted.
-- name: GetRegionForShare :one
SELECT * FROM region
WHERE id = $1 AND deleted_at IS NULL
FOR SHARE;

-- name: ListRegions :many
SELECT * FROM region
WHERE deleted_at IS NULL
ORDER BY id
LIMIT $1 OFFSET $2;

-- name: CountRegions :one
SELECT count(*) FROM region
WHERE deleted_at IS NULL;

-- name: GetRegionsByIDs :many
SELECT * FROM region
WHERE id = ANY(@ids::text[]) AND deleted_at IS NULL
ORDER BY id;

-- name: UpdateRegion :one
UPDATE region
SET name = $2, currency_code = $3, automatic_taxes = $4, tax_rate = $5, updated_at = $6
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteRegion :one
UPDATE region
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- GetRegionByCountry goes from country to region in ONE round trip.
--
-- It is the path used while a cart is being created (ResolveRegionForCountry).
-- No distinction is drawn between the country itself not being found and its
-- region not being found; which of the two holds is separated out by the
-- service with a second query ONLY on the error path. That is what keeps the
-- happy path a single query.
-- name: GetRegionByCountry :one
SELECT r.* FROM country c
JOIN region r ON r.id = c.region_id AND r.deleted_at IS NULL
WHERE c.iso_2 = $1;
