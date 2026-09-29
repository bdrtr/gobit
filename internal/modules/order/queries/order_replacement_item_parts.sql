-- order_replacement_item_parts queries (ADR 0238).

-- name: CreateOrderReplacementItemParts :exec
-- Writes a replacement item's parts; the position in the arrays is the rank.
INSERT INTO order_replacement_item_parts (order_replacement_item_id, variant_id, quantity, rank)
SELECT sqlc.arg('item_id')::text, p.variant_id, (sqlc.arg('quantities')::bigint[])[p.ordinality],
       (p.ordinality - 1)::integer
FROM unnest(sqlc.arg('variant_ids')::text[]) WITH ORDINALITY AS p(variant_id, ordinality);

-- name: ListOrderReplacementItemParts :many
-- The parts of every item of a replacement, each item's in its rank.
SELECT p.order_replacement_item_id, p.variant_id, p.quantity, p.reservation_id
FROM order_replacement_item_parts AS p
JOIN order_replacement_items AS i ON i.id = p.order_replacement_item_id
WHERE i.order_replacement_id = sqlc.arg('replacement_id')::text
ORDER BY p.order_replacement_item_id, p.rank;

-- name: SetOrderReplacementItemPartReservation :execrows
-- Writes the promise one part's units are held under.
UPDATE order_replacement_item_parts
SET reservation_id = sqlc.arg('reservation_id')::text, updated_at = now()
WHERE order_replacement_item_id = sqlc.arg('item_id')::text AND variant_id = sqlc.arg('variant_id')::text;

-- ClearOrderReplacementItemPartReservations forgets the promises a recalled
-- replacement's parts held (ADR 0239).
-- name: ClearOrderReplacementItemPartReservations :exec
UPDATE order_replacement_item_parts AS p
SET reservation_id = NULL, updated_at = now()
FROM order_replacement_items AS i
WHERE i.id = p.order_replacement_item_id
  AND i.order_replacement_id = sqlc.arg('replacement_id')::text
  AND p.reservation_id IS NOT NULL;
