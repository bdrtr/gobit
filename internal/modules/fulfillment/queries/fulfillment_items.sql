-- fulfillment_items queries.
--
-- An item carries the id of an order line item; that id belongs to ANOTHER
-- module and is not validated here (Principle 2.2).

-- name: CreateFulfillmentItem :one
INSERT INTO fulfillment_items (id, fulfillment_id, line_item_id, quantity)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- ListFulfillmentItems returns a parcel's items in the order they were written:
-- they share a created_at, and seq is the order the write loop gave them
-- (D175, the rule ADR 0233 gave an order's lines).
-- name: ListFulfillmentItems :many
SELECT * FROM fulfillment_items
WHERE fulfillment_id = $1
ORDER BY created_at, seq;

-- ListFulfillmentItemsByFulfillments returns the items for MULTIPLE
-- fulfillments in a single round trip; the list endpoints do not issue a query
-- per fulfillment (no N+1). Each parcel's items come in the order
-- ListFulfillmentItems gives them.
-- name: ListFulfillmentItemsByFulfillments :many
SELECT * FROM fulfillment_items
WHERE fulfillment_id = ANY (sqlc.arg('fulfillment_ids')::text[])
ORDER BY fulfillment_id, created_at, seq;

-- CommittedQuantitiesForFulfillments sums, per order line, the units of that
-- line that a live OUTGOING parcel holds; a parcel bringing a return back never
-- left with the order's goods (ADR 0384).
--
-- "Live" means not canceled: a canceled parcel's goods never left the building,
-- so its units are still in the warehouse and still sellable. A parcel that came
-- back undelivered counts whole. The one reader in this tree is the backorder
-- claim at the checkout (gap D268); the counts that open a parcel and restock a
-- write-off are HeldQuantitiesForReference's, which tells such a parcel apart
-- (ADR 0423).
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
  AND f.return_id IS NULL
GROUP BY i.line_item_id
ORDER BY i.line_item_id;

-- HeldQuantitiesForReference sums, per order line, the units of the outgoing
-- parcels opened for one reference, by the reference this module stores rather
-- than by the order's link (ADR 0409, gap D265), in two figures (ADR 0423):
--
--   - live: the pending, shipped and delivered parcels', which hold their units,
--     and those of a parcel that came back before ADR 0423 (held_whole,
--     migration 000008), which holds them as it did then;
--   - back: the other returned parcels', which came back to the sender
--     undelivered and hold their units only as far as a return or a
--     replacement speaks for them.
--
-- A canceled parcel is in neither: its goods never left. It is read under the
-- reference's dispatch lock, by a parcel opening and by a cancellation (ADR
-- 0420), and outside it by the panel's offer.
-- name: HeldQuantitiesForReference :many
SELECT i.line_item_id,
       COALESCE(SUM(i.quantity) FILTER (WHERE f.status IN ('pending', 'shipped', 'delivered')
                                           OR (f.status = 'returned' AND f.held_whole)), 0)::bigint AS live,
       COALESCE(SUM(i.quantity) FILTER (WHERE f.status = 'returned' AND NOT f.held_whole), 0)::bigint AS back
FROM fulfillment_items i
JOIN fulfillments f ON f.id = i.fulfillment_id
WHERE f.reference = sqlc.arg('reference')
  AND f.status IN ('pending', 'shipped', 'delivered', 'returned')
  AND f.return_id IS NULL
GROUP BY i.line_item_id
ORDER BY i.line_item_id;

-- ReturningQuantitiesForReturn sums, per order line, the units the live parcels
-- bringing one return back hold (ADR 0384).
--
-- "Live" is pending, shipped or delivered. A canceled parcel never traveled and
-- a returned one came back to the customer undelivered, so neither holds any of
-- the return's units any more.
-- name: ReturningQuantitiesForReturn :many
SELECT i.line_item_id, SUM(i.quantity)::bigint AS quantity
FROM fulfillment_items i
JOIN fulfillments f ON f.id = i.fulfillment_id
WHERE f.return_id = sqlc.arg('return_id')::text
  AND f.status IN ('pending', 'shipped', 'delivered')
GROUP BY i.line_item_id
ORDER BY i.line_item_id;
