-- order_line_cancellations queries: units of a line that will not be delivered.

-- name: CreateOrderLineCancellation :one
INSERT INTO order_line_cancellations (id, order_line_item_id, quantity, reason, note)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- ListOrderLineCancellations returns an order's cancellations, oldest first.
--
-- The order is reached through the LINE: the cancellation names the line and the
-- line names the order, so nothing here can disagree with anything.
-- name: ListOrderLineCancellations :many
SELECT c.* FROM order_line_cancellations c
JOIN order_line_items l ON l.id = c.order_line_item_id
WHERE l.order_id = $1
ORDER BY c.created_at, c.id;

-- SumCanceledQuantities reports how many units of each of the given order lines
-- have already been written off.
--
-- It is the other half of SumReturnedQuantities, and the two are read together:
-- a unit is spoken for once it has been asked back OR canceled, and a ceiling
-- that counted only one of them would let the same unit be claimed twice.
--
-- There is no status to exclude. A cancellation has no lifecycle -- it is a fact
-- about goods, written once -- so every row counts, and taking one back means
-- deleting it, which nothing does.
-- name: SumCanceledQuantities :many
SELECT c.order_line_item_id, SUM(c.quantity)::bigint AS canceled
FROM order_line_cancellations c
WHERE c.order_line_item_id = ANY(sqlc.arg('line_item_ids')::text[])
GROUP BY c.order_line_item_id;
