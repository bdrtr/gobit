-- order_replacement_items queries: which lines are being replaced.

-- Exactly ONE of order_line_item_id and variant_id is set; the CHECK in
-- migration 000019 holds it, and the service decides which shape a request has.
-- name: CreateOrderReplacementItem :one
INSERT INTO order_replacement_items
    (id, order_replacement_id, order_line_item_id, variant_id, quantity)
VALUES ($1, $2, $3, $4, $5)
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
--
-- # Variant-shaped rows are NOT counted, and cannot be
--
-- Since migration 000019 a replacement item may name a VARIANT instead of a line
-- (ADR 0145). The ceiling this sum feeds is "no more of a line than was bought on
-- it", and a row that names no line is not against any line's ceiling — it is
-- goods the order never sold. The `IS NOT NULL` is therefore a statement rather
-- than a filter: counting such a row would attribute it to a line chosen by
-- nothing.
--
-- What bounds a variant-shaped row instead is written where the decision is: the
-- exchange's money guard, which refuses a dispatch until the difference is funded.
-- name: SumReplacedQuantities :many
SELECT i.order_line_item_id, SUM(i.quantity)::bigint AS replaced
FROM order_replacement_items i
JOIN order_replacements r ON r.id = i.order_replacement_id
WHERE i.order_line_item_id = ANY(sqlc.arg('line_item_ids')::text[])
  AND i.order_line_item_id IS NOT NULL
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
