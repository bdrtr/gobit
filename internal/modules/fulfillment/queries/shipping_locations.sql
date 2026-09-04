-- shipping_locations and shipping_location_regions queries.
--
-- The read path splits in two, and the split rests on the SAME rationale as the
-- one in shipping_options:
--
--   - Admin listing (ListShippingLocations) — paginated.
--   - Selection read (ShippingLocationPolicies) — returns, in a single round
--     trip, the FACTS about the candidate locations that affect the decision.
--     The policy ITSELF does not run here; elimination and ordering live in the
--     pure function in the service layer and can be proven by a unit test
--     without a database.
--
-- Deletion is NOT SOFT; its rationale is at the top of the migration. That is
-- why none of the queries carries a deleted_at filter, and its absence is not
-- an oversight.

-- UpsertShippingLocation WRITES the policy or writes OVER it.
--
-- The reason there is a single upsert instead of a separate Create/Update pair
-- is that what the surface expresses is not creating an ENTITY but settling the
-- SETTING that belongs to a location: the caller says "this location has this
-- priority" and whether the row existed before is not its problem.
-- name: UpsertShippingLocation :one
INSERT INTO shipping_locations (location_id, priority)
VALUES ($1, $2)
ON CONFLICT (location_id) DO UPDATE
    SET priority = EXCLUDED.priority, updated_at = now()
RETURNING *;

-- GetShippingLocation returns the policy TOGETHER WITH ITS REGIONS in a single
-- statement.
--
-- Reading it with two separate SELECTs (the row first, then the links) would
-- produce a torn record: two reads made outside a transaction come from two
-- different snapshots, and a write landing between them would show the NEW
-- priority of the location next to its OLD regions. The write path closes this
-- with a transaction; the read path closes it with a single statement. The
-- pattern is identical to the one in ShippingLocationPolicies.
-- name: GetShippingLocation :one
SELECT
    l.location_id,
    l.priority,
    l.created_at,
    l.updated_at,
    COALESCE(
        array_agg(r.region_id ORDER BY r.region_id) FILTER (WHERE r.region_id IS NOT NULL),
        '{}'
    )::text[] AS region_ids
FROM shipping_locations l
LEFT JOIN shipping_location_regions r ON r.location_id = l.location_id
WHERE l.location_id = $1
GROUP BY l.location_id, l.priority, l.created_at, l.updated_at;

-- ListShippingLocations paginates the policies together with their links.
--
-- Here too the links are gathered in the SAME statement; no second query per
-- location on the page (N+1) is made, and the same tearing door the single read
-- closes stays closed.
-- name: ListShippingLocations :many
SELECT
    l.location_id,
    l.priority,
    l.created_at,
    l.updated_at,
    COALESCE(
        array_agg(r.region_id ORDER BY r.region_id) FILTER (WHERE r.region_id IS NOT NULL),
        '{}'
    )::text[] AS region_ids
FROM shipping_locations l
LEFT JOIN shipping_location_regions r ON r.location_id = l.location_id
GROUP BY l.location_id, l.priority, l.created_at, l.updated_at
ORDER BY l.priority, l.location_id
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountShippingLocations counts the SAME set as ListShippingLocations; the two
-- are obliged to change together.
-- name: CountShippingLocations :one
SELECT COUNT(*) FROM shipping_locations;

-- DeleteShippingLocation deletes the policy PERMANENTLY; the region links fall
-- with it through ON DELETE CASCADE.
--
-- Deleting does NOT mean "close the location", it means "return the location to
-- the default": a location that has no policy counts as being at the default
-- priority and as serving every region.
-- name: DeleteShippingLocation :execrows
DELETE FROM shipping_locations
WHERE location_id = $1;

-- ShippingLocationPolicies returns the facts about the candidate locations that
-- affect the decision in a SINGLE round trip; no query per candidate (N+1) is
-- made.
--
-- The returned rows are ONLY for the locations that have a policy. A candidate
-- that is not in the list is "without a policy" and counts as the default
-- (priority 0, serves every region); the caller draws that distinction, not the
-- query.
--
-- The region links are returned as an ARRAY OF IDS, not as a COUNT or a FLAG,
-- and this is deliberate. There are two reasons for it: the rule ("one with no
-- link serves every region, one that has links serves only the ones it is
-- linked to") stays testable in a pure function, without a database; and when
-- every candidate is eliminated the error message can write down which regions
-- the locations are ACTUALLY linked to. The second is not a convenience but the
-- only route to a diagnosis: the id of a region that was deleted and reopened
-- matches nowhere, and with a query returning a flag the operator would only
-- see "no location serves it".
--
-- FILTER produces '{}' for a location that has no link; without it the LEFT
-- JOIN returns an array whose single element is NULL, and "has no link" could
-- not be told apart from "its link is NULL".
-- name: ShippingLocationPolicies :many
SELECT
    l.location_id,
    l.priority,
    COALESCE(
        array_agg(r.region_id ORDER BY r.region_id) FILTER (WHERE r.region_id IS NOT NULL),
        '{}'
    )::text[] AS region_ids
FROM shipping_locations l
LEFT JOIN shipping_location_regions r ON r.location_id = l.location_id
WHERE l.location_id = ANY (sqlc.arg('location_ids')::text[])
GROUP BY l.location_id, l.priority
ORDER BY l.location_id;

-- ReplaceShippingLocationRegions writes a location's region links WHOLESALE:
-- first all of them are deleted, then the given ones are written. The caller
-- has to call the two in the SAME transaction, otherwise a read landing in
-- between would see the location without regions (that is, open to every
-- region).
-- name: DeleteShippingLocationRegions :exec
DELETE FROM shipping_location_regions
WHERE location_id = $1;

-- InsertShippingLocationRegions writes the region links in a SINGLE statement;
-- no INSERT per region is made. A repeated region id is swallowed silently
-- (ON CONFLICT DO NOTHING) because "linking the same region twice" gives the
-- same result as the thing that was meant to be expressed, and meeting the
-- caller with a conflict error would be misleading.
-- name: InsertShippingLocationRegions :exec
INSERT INTO shipping_location_regions (location_id, region_id)
SELECT sqlc.arg('location_id')::text, region
FROM unnest(sqlc.arg('region_ids')::text[]) AS region
ON CONFLICT (location_id, region_id) DO NOTHING;

