-- order_replacement_items queries: which lines are being replaced.

-- name: CreateOrderReplacementItem :one
INSERT INTO order_replacement_items
    (id, order_replacement_id, order_line_item_id, quantity)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- ListOrderReplacementItems returns a replacement's lines in the order they
-- were written.
-- name: ListOrderReplacementItems :many
SELECT * FROM order_replacement_items
WHERE order_replacement_id = $1
ORDER BY created_at, id;

-- SumReplacedQuantities reports how many units of each of the given order lines
-- have ALREADY been promised, across every live replacement of the order.
--
-- # Why canceled replacements are excluded
--
-- A withdrawn promise releases the units it was holding: they can be promised
-- again. An open one must count, or two requests could each promise the whole
-- line and together send twice what was bought.
--
-- The service reads this under the order's lock and compares it against the
-- ordered quantity, exactly as SumReturnedQuantities is used. It has to be a
-- query rather than a CHECK because the rule spans rows.
-- name: SumReplacedQuantities :many
SELECT i.order_line_item_id, SUM(i.quantity)::bigint AS replaced
FROM order_replacement_items i
JOIN order_replacements r ON r.id = i.order_replacement_id
WHERE i.order_line_item_id = ANY(sqlc.arg('line_item_ids')::text[])
  AND r.status <> 'canceled'
GROUP BY i.order_line_item_id;

-- SetOrderReplacementItemReservation writes the promise a line's units are held
-- under.
--
-- It is written BEFORE the units are confirmed, so a dispatch that dies between
-- the two finds the promise on the row instead of making a second one.
-- name: SetOrderReplacementItemReservation :one
UPDATE order_replacement_items
SET reservation_id = sqlc.arg('reservation_id')::text,
    updated_at = now()
WHERE id = sqlc.arg('id')::text
RETURNING *;
