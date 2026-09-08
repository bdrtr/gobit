-- stock_locations queries.
--
-- A location is RETIRED BY CLOSING it, not by deleting it (ADR 0055), so the
-- reads here do not all carry one filter the way the other tables' do. The
-- single-row read deliberately answers for a closed location — every level and
-- every reservation names it — and the listing takes the choice as a parameter,
-- because the operator stocking goods and the operator reading last year's
-- reservation are asking different questions.

-- name: CreateStockLocation :one
INSERT INTO stock_locations (
    id, name, address_1, address_2, city, province, postal_code, country_code
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- GetStockLocation returns the location whether it is open or CLOSED.
--
-- The missing filter is the decision, not an omission. inventory_levels and
-- inventory_reservations point here, reservation rows are never deleted, and a
-- location the single-row read refused to answer for would leave that history
-- naming a warehouse nobody can name back.
-- name: GetStockLocation :one
SELECT * FROM stock_locations
WHERE id = $1;

-- LockStockLocation locks the location row EXCLUSIVELY until the end of the
-- transaction and returns it.
--
-- The close takes this lock, and it is the first lock of the whole module's
-- order (see the lock order section on the service's Store). Closing decides
-- against what the location holds, so the flows that put stock INTO a location
-- have to be held off while it decides; without this row as the rendezvous
-- point, a stock write committed after the close's count and before its stamp
-- would leave units standing in a location that no availability read ever
-- joins.
-- name: LockStockLocation :one
SELECT * FROM stock_locations
WHERE id = $1
FOR UPDATE;

-- LockStockLocationShared locks the location row in SHARED mode and returns it.
--
-- Every flow that writes stock at a location takes this first. Shared, so two
-- warehouse operators stocking different items at the same location do not
-- queue behind each other; it collides only with the close's exclusive lock.
-- name: LockStockLocationShared :one
SELECT * FROM stock_locations
WHERE id = $1
FOR SHARE;

-- ListStockLocations pages the locations, closed ones only when asked for.
--
-- The default hides them because the list is what an operator picks a warehouse
-- FROM, and a closed location cannot be picked: it takes no stock. Asking for
-- them is still possible, so a decommissioned warehouse stays findable: the
-- levels and the reservations of the past name it, and a reader of that history
-- has to be able to look it up.
-- name: ListStockLocations :many
SELECT * FROM stock_locations
WHERE (closed_at IS NULL OR sqlc.arg('include_closed')::boolean)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountStockLocations gives the total for the pagination envelope; it applies
-- the same filter as ListStockLocations.
--
-- The count is a separate query: a window function returned alongside the rows
-- would show the total as 0 on an out-of-range page, because no row comes back
-- there.
-- name: CountStockLocations :one
SELECT COUNT(*) FROM stock_locations
WHERE (closed_at IS NULL OR sqlc.arg('include_closed')::boolean);

-- CloseStockLocation stamps the location closed.
--
-- The statement carries NO "closed_at IS NULL" guard, and the reason is that
-- the decision is already made: the caller holds the exclusive lock, has read
-- the row and only reaches this statement for a location that is open, so a
-- second close never arrives here and the first closing moment cannot be
-- overwritten by it. A guard here would be a second place deciding the same
-- thing, and the two could disagree.
-- name: CloseStockLocation :one
UPDATE stock_locations
SET closed_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;
