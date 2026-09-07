-- inventory_items queries.

-- name: CreateInventoryItem :one
INSERT INTO inventory_items (
    id, sku, title, description, requires_shipping
) VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetInventoryItem :one
SELECT * FROM inventory_items
WHERE id = $1 AND deleted_at IS NULL;

-- LockInventoryItem locks the item for the duration of a transaction; the flows
-- that CREATE an (item, location) level use it. The lock stops two concurrent
-- creations of the same item from colliding on the unique index: the one that
-- will create the row wins the race here, and the other waits and then sees the
-- row that already exists.
-- name: LockInventoryItem :one
SELECT id FROM inventory_items
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- LockInventoryItemShared takes a SHARED lock on the item; the flows that touch
-- level and reservation rows (Reserve/Release/Confirm/Adjust) use it as the
-- first step of the LOCK ORDER.
--
-- There is one lock order and it is the same in every flow: the item first,
-- then the level. Reversing it means a deadlock; the foreign key the
-- reservation row gives the item already asks for an implicit FOR KEY SHARE
-- lock, which is to say that if the order were not taken explicitly here it
-- would be taken in the reverse order at the moment of the INSERT.
--
-- The lock is SHARED (FOR KEY SHARE): two concurrent reservations do not wait
-- on each other — the FOR UPDATE lock on the level row already serialises them.
-- The flows that change the item structurally (SetInventoryLevel,
-- DeleteInventoryItem) take FOR UPDATE, so they do CONFLICT with this lock and
-- the order is preserved.
-- name: LockInventoryItemShared :one
SELECT id FROM inventory_items
WHERE id = $1 AND deleted_at IS NULL
FOR KEY SHARE;

-- name: ListInventoryItems :many
SELECT * FROM inventory_items
WHERE deleted_at IS NULL
  AND (sqlc.narg('sku')::text IS NULL OR sku = sqlc.narg('sku')::text)
  AND (sqlc.narg('requires_shipping')::boolean IS NULL
       OR requires_shipping = sqlc.narg('requires_shipping')::boolean)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountInventoryItems gives the total for the pagination envelope and applies
-- the SAME filters as ListInventoryItems; the two have to be changed together.
--
-- The total cannot be read from a window function returned alongside the rows
-- (COUNT(*) OVER ()): on an out-of-range page no row comes back, the window is
-- never evaluated, and the total would read 0. The total is the count of the
-- FILTER, not of the page; that is why it is a separate query, independent of
-- the pagination.
-- name: CountInventoryItems :one
SELECT COUNT(*) FROM inventory_items
WHERE deleted_at IS NULL
  AND (sqlc.narg('sku')::text IS NULL OR sku = sqlc.narg('sku')::text)
  AND (sqlc.narg('requires_shipping')::boolean IS NULL
       OR requires_shipping = sqlc.narg('requires_shipping')::boolean);

-- GetInventoryItemsByIDs answers the Query layer's FetchByIDs call in a SINGLE
-- round trip; no query is made per id (N+1).
-- name: GetInventoryItemsByIDs :many
SELECT * FROM inventory_items
WHERE id = ANY (sqlc.arg('ids')::text[]) AND deleted_at IS NULL
ORDER BY id;

-- name: SoftDeleteInventoryItem :execrows
UPDATE inventory_items
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;
