-- order_return_items queries: which lines of the order are coming back.

-- name: CreateOrderReturnItem :one
INSERT INTO order_return_items (id, order_return_id, order_line_item_id, quantity, refund_amount)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- ListOrderReturnItems returns a return's lines in the order they were written.
-- name: ListOrderReturnItems :many
SELECT * FROM order_return_items
WHERE order_return_id = $1 AND deleted_at IS NULL
ORDER BY created_at, id;

-- SumReturnedQuantities reports how many units of each of the given order lines
-- have ALREADY been asked back, across every live return of the order.
--
-- # Why canceled returns are excluded and received ones are not
--
-- A withdrawn request releases the units it was holding: they can be asked back
-- again. A received one cannot — those goods are physically here — and a
-- requested one must count too, or two open requests could each claim the whole
-- line and together ask back twice what was bought.
--
-- The service reads this under the order's lock and compares it against the
-- ordered quantity. It has to be a query rather than a CHECK because the rule
-- spans rows, and a CHECK cannot see rows other than its own.
-- name: SumReturnedQuantities :many
SELECT i.order_line_item_id, SUM(i.quantity)::bigint AS returned
FROM order_return_items i
JOIN order_returns r ON r.id = i.order_return_id
WHERE i.order_line_item_id = ANY(sqlc.arg('line_item_ids')::text[])
  AND i.deleted_at IS NULL
  AND r.deleted_at IS NULL
  AND r.status <> 'canceled'
GROUP BY i.order_line_item_id;
