-- inventory_backorders queries (ADR 0392).
--
-- A claim is written by the checkout, filled by a write that makes units
-- sellable, and withdrawn by a write-off. It is never deleted: the row is what a
-- filled line's write-off reads to find the shelf its units left.

-- CreateBackorder records a claim, or nothing when the line already has one for
-- the item: a recovery that runs the checkout's last step again reads the first.
-- name: CreateBackorder :one
INSERT INTO inventory_backorders (
    id, inventory_item_id, order_id, order_line_item_id, quantity, location_ids
) VALUES ($1, $2, $3, $4, $5, sqlc.arg('location_ids')::text[])
ON CONFLICT (order_line_item_id, inventory_item_id) DO NOTHING
RETURNING *;

-- name: GetBackorderOfLine :one
SELECT * FROM inventory_backorders
WHERE order_line_item_id = $1 AND inventory_item_id = $2;

-- LockBackordersOfLine locks one line's claims, in queue order, for a
-- settlement.
-- name: LockBackordersOfLine :many
SELECT * FROM inventory_backorders
WHERE order_line_item_id = $1
ORDER BY seq
FOR UPDATE;

-- LockWaitingBackordersAt locks the claims a fill at one level can complete: the
-- item's waiting claims that name the location and owe no more than is sellable
-- there, in queue order.
--
-- Rows are locked in seq order, so two fills cannot deadlock on them, and under
-- READ COMMITTED a row that changed while this waited is checked against the
-- predicate again. The fit predicate keeps a fill from locking claims it cannot
-- complete, so a large claim does not hold up a settlement of its line.
-- name: LockWaitingBackordersAt :many
SELECT * FROM inventory_backorders
WHERE inventory_item_id = sqlc.arg('inventory_item_id')::text
  AND status = 'waiting'
  AND sqlc.arg('location_id')::text = ANY (location_ids)
  AND quantity - withdrawn_quantity <= sqlc.arg('available')::bigint
ORDER BY seq
FOR UPDATE;

-- FillBackorder marks a waiting claim filled by the reservation at the location.
-- name: FillBackorder :execrows
UPDATE inventory_backorders
SET status = 'filled', reservation_id = sqlc.arg('reservation_id')::text,
    filled_location_id = sqlc.arg('location_id')::text, updated_at = now()
WHERE id = sqlc.arg('id')::text AND status = 'waiting';

-- WithdrawBackorder writes how many of a waiting claim's units the order will no
-- longer take.
-- name: WithdrawBackorder :one
UPDATE inventory_backorders
SET withdrawn_quantity = sqlc.arg('withdrawn_quantity')::bigint,
    status = sqlc.arg('status')::text, updated_at = now()
WHERE id = sqlc.arg('id')::text
RETURNING *;

-- name: CountWaitingBackorders :one
SELECT COUNT(*) FROM inventory_backorders
WHERE inventory_item_id = $1 AND status = 'waiting';

-- ListBackorders pages one item's claims in queue order, every status or one.
-- name: ListBackorders :many
SELECT * FROM inventory_backorders
WHERE inventory_item_id = sqlc.arg('inventory_item_id')::text
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY seq
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- name: CountBackorders :one
SELECT COUNT(*) FROM inventory_backorders
WHERE inventory_item_id = sqlc.arg('inventory_item_id')::text
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text);
