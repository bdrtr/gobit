-- inventory_levels queries.
--
-- The available quantity is read from a STORED column in no query here; it is
-- derived everywhere as stocked_quantity - reserved_quantity.

-- LockInventoryLevel locks the level row for the duration of the transaction.
--
-- THIS IS THE BASIS OF THE CONCURRENCY. Two concurrent reservations are obliged
-- to lock the same row; the second waits until the first one's transaction ends
-- and, under READ COMMITTED, sees the CURRENT version of the row. That is why a
-- "read first, then write" race cannot arise: the read is already made after
-- the lock.
-- name: LockInventoryLevel :one
SELECT * FROM inventory_levels
WHERE inventory_item_id = $1 AND location_id = $2 AND deleted_at IS NULL
FOR UPDATE;

-- name: CreateInventoryLevel :one
INSERT INTO inventory_levels (
    id, inventory_item_id, location_id, stocked_quantity, reserved_quantity
) VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateInventoryLevelQuantities :one
UPDATE inventory_levels
SET stocked_quantity = $2, reserved_quantity = $3, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: ListInventoryLevels :many
SELECT * FROM inventory_levels
WHERE inventory_item_id = $1 AND deleted_at IS NULL
ORDER BY created_at, id;

-- AvailableQuantityByItemIDs returns, per item, the available total over ALL
-- locations in one round trip; the Query provider uses it.
-- For an item that has no level at all NO ROW comes back; the caller counts it
-- as zero.
-- name: AvailableQuantityByItemIDs :many
SELECT inventory_item_id,
       COALESCE(SUM(stocked_quantity - reserved_quantity), 0)::bigint AS available_quantity
FROM inventory_levels
WHERE inventory_item_id = ANY (sqlc.arg('ids')::text[]) AND deleted_at IS NULL
GROUP BY inventory_item_id;

-- name: SoftDeleteInventoryLevelsByItem :exec
UPDATE inventory_levels
SET deleted_at = now(), updated_at = now()
WHERE inventory_item_id = $1 AND deleted_at IS NULL;
