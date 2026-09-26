-- order_delivery_changes queries (ADR 0199).
--
-- A change is written once and never updated: the method's current delivery is
-- its latest change, and the earlier ones are what it was before.

-- name: CreateDeliveryChange :one
INSERT INTO order_delivery_changes (
    id, order_id, shipping_method_id, shipping_option_id, name, amount, difference,
    credit_line_id, payment_collection_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- ListDeliveryChanges reads the changes of several orders in ONE query, oldest
-- first within an order.
-- name: ListDeliveryChanges :many
SELECT * FROM order_delivery_changes
WHERE order_id = ANY (sqlc.arg('order_ids')::text[])
ORDER BY order_id, created_at, id;

-- DeliveryChangePaidBy names the change a collection paid for, if any
-- (ADR 0200).
-- name: DeliveryChangePaidBy :many
SELECT id FROM order_delivery_changes
WHERE payment_collection_id = sqlc.arg('payment_collection_id')
LIMIT 1;
