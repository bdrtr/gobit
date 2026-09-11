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

-- CommittedQuantitiesForFulfillments sums, per order line, the units of that
-- line that a live parcel holds.
--
-- "Live" means not canceled: a canceled parcel's goods never left the building,
-- so its units are still in the warehouse and still sellable, while a shipped,
-- delivered or even RETURNED parcel's units did leave. A returned one coming back
-- is the return flow's receipt and puts its own stock back, so counting it here
-- as still gone is the answer that leaves each act with one effect.
--
-- 'pending' counts as gone as well. That is deliberate: a pending parcel is one
-- the warehouse is already picking, its stock was deducted at checkout, and
-- treating those units as available would let a cancellation put back goods that
-- are in a box.
-- name: CommittedQuantitiesForFulfillments :many
SELECT i.line_item_id, SUM(i.quantity)::bigint AS quantity
FROM fulfillment_items i
JOIN fulfillments f ON f.id = i.fulfillment_id
WHERE i.fulfillment_id = ANY (sqlc.arg('fulfillment_ids')::text[])
  AND f.status <> 'canceled'
GROUP BY i.line_item_id
ORDER BY i.line_item_id;
