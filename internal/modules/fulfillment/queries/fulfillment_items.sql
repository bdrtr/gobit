-- fulfillment_items queries.
--
-- An item carries the id of an order line item; that id belongs to ANOTHER
-- module and is not validated here (Principle 2.2).

-- name: CreateFulfillmentItem :one
INSERT INTO fulfillment_items (id, fulfillment_id, line_item_id, quantity)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListFulfillmentItems :many
SELECT * FROM fulfillment_items
WHERE fulfillment_id = $1
ORDER BY id;

-- ListFulfillmentItemsByFulfillments returns the items for MULTIPLE
-- fulfillments in a single round trip; the list endpoints do not issue a query
-- per fulfillment (no N+1).
-- name: ListFulfillmentItemsByFulfillments :many
SELECT * FROM fulfillment_items
WHERE fulfillment_id = ANY (sqlc.arg('fulfillment_ids')::text[])
ORDER BY fulfillment_id, id;
