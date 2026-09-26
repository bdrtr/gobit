-- order_shipping_methods queries (ADR 0198).
--
-- There is no UPDATE and no DELETE: a method is written with the order and is
-- the permanent answer to which delivery was sold, as the order's lines are.

-- CreateOrderShippingMethod writes one method INSIDE the order's transaction.
-- name: CreateOrderShippingMethod :one
INSERT INTO order_shipping_methods (id, order_id, shipping_option_id, name, amount)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- ListOrderShippingMethods reads the methods of several orders in ONE query.
-- name: ListOrderShippingMethods :many
SELECT * FROM order_shipping_methods
WHERE order_id = ANY (sqlc.arg('order_ids')::text[])
ORDER BY order_id, created_at, id;
