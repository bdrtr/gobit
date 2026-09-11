-- inventory_movements queries.
--
-- The ledger is APPEND-ONLY. There is no UPDATE and no DELETE here, and that is
-- not an omission waiting to be filled: a movement records something that
-- happened, and a row that can be rewritten explains nothing. The rows leave
-- only with the item or the location they name, through the CASCADEs the
-- migration declares.

-- AppendMovement writes the row that explains one change to the physical count.
--
-- It is called INSIDE the transaction that writes inventory_levels — the
-- repository refuses it outside one — so the level and its explanation commit
-- together or neither does.
-- The ON CONFLICT clause is GONE, together with the unique index it inferred
-- (migration 000007). It held one cancellation movement per reference, which was
-- the idempotency of the old design: an act added a delta once. The delta is no
-- longer what an act computes — it brings the line's total UP TO a target read
-- under the level's lock — so the same reference can legitimately appear twice
-- and a redelivered event writes nothing because the target is already met.
-- name: AppendMovement :one
INSERT INTO inventory_movements (
    id, inventory_item_id, location_id, reservation_id, reason, delta, stocked_after,
    reference, line_item_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- ReturnedForLine sums the units a line's write-offs have already put back, for
-- one inventory item.
--
-- It is the question that makes the two acts order-independent: whichever runs
-- first brings the total to its target, and the other recomputes the target from
-- current state and tops up by the difference. Read INSIDE the transaction that
-- holds the level's lock, so two concurrent calls cannot both see the old sum.
--
-- # Why the ITEM is in the predicate
--
-- Because the lock is. The caller holds the level of one (item, location) pair,
-- and a sum that reached across items would be reading rows nothing in this
-- transaction protects. In production a line sells one variant and a variant
-- tracks one item, so the item narrows nothing — it makes the read match the
-- lock, which is a property rather than a filter. The integration suite found
-- this the way it finds things: the test passed alone and failed beside its
-- neighbour, which was reusing the line id on a different item.
--
-- COALESCE, because a line nothing has returned yet has no rows rather than a
-- row of zero.
-- name: ReturnedForLine :one
SELECT COALESCE(SUM(delta), 0)::bigint AS returned
FROM inventory_movements
WHERE reason = 'cancellation'
  AND line_item_id = sqlc.arg('line_item_id')
  AND inventory_item_id = sqlc.arg('inventory_item_id');

-- ListMovementsForItem pages one item's movements, newest first.
--
-- This is the operator's question — "what happened to this item" — and
-- inventory_movements_item_idx is that question as an index: the item narrows,
-- and (created_at, id) DESC is both the order and the keyset position.
--
-- The keyset is written with a SENTINEL rather than with the nullable OR form
-- (`sqlc.narg('after_id') IS NULL OR (created_at, id) < (...)`), because
-- Postgres folds the OR away in a custom plan and keeps it as a Filter in a
-- generic one — so the seek measures well five times and then becomes a full
-- index walk with nothing in the code having changed. See internal/core/page.
--
-- The location filter is deliberately NOT an index of its own. It runs inside
-- the rows the item has already narrowed to, and an item has as many levels as
-- the shop has warehouses.
-- name: ListMovementsForItem :many
SELECT * FROM inventory_movements
WHERE inventory_item_id = sqlc.arg('inventory_item_id')::text
  AND (sqlc.narg('location_id')::text IS NULL
       OR location_id = sqlc.narg('location_id')::text)
  AND (created_at, id) < (
    COALESCE(sqlc.narg('after_at')::timestamptz, 'infinity'::timestamptz),
    COALESCE(sqlc.narg('after_id')::text, '')
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint;

-- SaleLocationsForReference answers where an order's units were taken from.
--
-- One row per inventory item, holding the location the SALE deducted from. It is
-- the way back for a cancellation: the units have to return to the shelf they
-- left, and the reservation that knew the location is keyed to the CART's line
-- item, which the order does not carry.
--
-- DISTINCT ON rather than a GROUP BY, because what is wanted is one location per
-- item and an order with two sale movements for one item at two locations has a
-- real answer for each — the first is taken and the caller is told nothing about
-- the second, which is a limit the record states rather than hides.
-- name: SaleLocationsForReference :many
SELECT DISTINCT ON (inventory_item_id) inventory_item_id, location_id
FROM inventory_movements
WHERE reason = 'sale' AND reference = sqlc.arg('reference')::text
ORDER BY inventory_item_id, created_at, id;
