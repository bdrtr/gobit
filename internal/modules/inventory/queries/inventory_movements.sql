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
-- name: AppendMovement :one
INSERT INTO inventory_movements (
    id, inventory_item_id, location_id, reservation_id, reason, delta, stocked_after
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

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
