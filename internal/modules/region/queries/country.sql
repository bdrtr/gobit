-- country queries.
--
-- The table has NO deleted_at and these reads carry NO such filter. country is
-- REFERENCE DATA: its rows are written by the seed in 000002, its lifecycle is
-- that migration's, and the only write path the module offers moves a country
-- BETWEEN regions. The column stood from 000001 until 000003 and was never once
-- written; the argument is at the top of 000003 (docs/gaps.md D18).

-- name: GetCountry :one
SELECT * FROM country
WHERE iso_2 = $1;

-- GetCountryForUpdate reads the country and locks its row UNTIL THE END OF THE
-- TRANSACTION.
--
-- It is the concurrency leg of the rule "a country may belong to at most one
-- region". In an unlocked "read first, then write" flow two requests adding the
-- same country to two different regions would BOTH see region_id empty, and the
-- second would silently overwrite what the first had written. The lock makes
-- the second one wait; when its wait is over it reads the CURRENT version of
-- the row and sees the conflict.
--
-- It is the second step of the lock order: region first (GetRegionForShare),
-- country second.
-- name: GetCountryForUpdate :one
SELECT * FROM country
WHERE iso_2 = $1
FOR UPDATE;

-- ListCountries returns countries a page at a time; the region filter is
-- optional.
--
-- A NULL region_id means "do not filter", a particular region id means that
-- region's countries. "Countries attached to no region at all" would be a
-- separate request and is deliberately not offered: for the administration
-- surface the full list is enough.
-- name: ListCountries :many
SELECT * FROM country
WHERE (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text)
ORDER BY iso_2
LIMIT @lim::integer OFFSET @off::integer;

-- name: CountCountries :one
SELECT count(*) FROM country
WHERE (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text);

-- ListCountriesByRegions reads the countries of several regions in ONE round
-- trip.
--
-- The query provider returns regions together with their countries; a separate
-- query per region would be an N+1 (ADR 0004's batch-read requirement).
-- name: ListCountriesByRegions :many
SELECT * FROM country
WHERE region_id = ANY(@region_ids::text[])
ORDER BY region_id, iso_2;

-- name: SetCountryRegion :one
UPDATE country
SET region_id = @region_id::text, updated_at = @updated_at::timestamptz
WHERE iso_2 = @iso_2::text
RETURNING *;

-- ClearCountryRegion detaches the country from its region.
--
-- The region id is part of the condition as well: a request that would
-- accidentally release another region's country finds no row and gets an error.
-- name: ClearCountryRegion :one
UPDATE country
SET region_id = NULL, updated_at = @updated_at::timestamptz
WHERE iso_2 = @iso_2::text AND region_id = @region_id::text
RETURNING *;

-- ClearRegionCountries releases ALL of a region's countries.
--
-- It is called while a region is being deleted. Were it not called, the
-- countries would stay attached to a dead region, could not be added to any
-- other region, and ResolveRegionForCountry would answer "not found" for them
-- for ever.
-- name: ClearRegionCountries :exec
UPDATE country
SET region_id = NULL, updated_at = @updated_at::timestamptz
WHERE region_id = @region_id::text;
