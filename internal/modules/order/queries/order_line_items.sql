-- order_line_items queries.
--
-- Order lines ARE IMMUTABLE ONCE WRITTEN: an order is the permanent answer to
-- the question "what was sold at that moment", and correcting a line's quantity
-- or amount afterwards would corrupt that answer. That is why there is NO UPDATE
-- query here; the path to a correction is a return/exchange record.

-- name: CreateOrderLineItem :one
INSERT INTO order_line_items (
    id, order_id, variant_id, title, quantity,
    unit_price, subtotal, discount_total, tax_total, tax_rate_bps, total, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: ListOrderLineItems :many
SELECT * FROM order_line_items
WHERE order_id = $1
ORDER BY created_at, id;

-- ListOrderLineItemsFiltered is the cross-module read of the LINE as an entity
-- of its own (the "order_line_item" Query provider).
--
-- # Why it joins orders when ListOrderLineItems does not
--
-- The DATE. This query's reason to exist is "which variants sold in this
-- period", and the moment a line was sold is the ORDER's placed_at, not the
-- line's created_at: created_at says when the row was written, and for a line
-- added later to an existing order (an exchange) the two are different days.
-- Filtering on the line's own stamp would answer a question nobody asked. Why
-- the date is not copied onto the line instead is argued in migration 000006.
--
-- Liveness used to be the second reason and is not one any more. It was the
-- order's deleted_at, and no order carries one: an order retires by STATUS and
-- the six columns are gone (ADR 0054). What replaced the condition is nothing,
-- because there is nothing left for it to hide.
--
-- The ORDER BY is o.placed_at DESC, li.id DESC: the analytics reader wants the
-- most recent sales first, and li.id breaks the tie so a page boundary does not
-- move between two calls. orders_placed_at_idx (migration 000006) serves both
-- the range and the ordering.
-- name: ListOrderLineItemsFiltered :many
SELECT li.* FROM order_line_items li
    JOIN orders o ON o.id = li.order_id
WHERE (sqlc.narg('order_id')::text IS NULL OR li.order_id = sqlc.narg('order_id')::text)
  AND (sqlc.narg('variant_id')::text IS NULL OR li.variant_id = sqlc.narg('variant_id')::text)
  AND (sqlc.narg('placed_from')::timestamptz IS NULL
       OR o.placed_at >= sqlc.narg('placed_from')::timestamptz)
  AND (sqlc.narg('placed_to')::timestamptz IS NULL
       OR o.placed_at < sqlc.narg('placed_to')::timestamptz)
ORDER BY o.placed_at DESC, li.id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- GetOrderLineItemsByIDs satisfies the Query layer's FetchByIDs call in a
-- SINGLE round trip; no per-ID query (N+1) is made.
--
-- It used to JOIN orders, and the join carried one condition: the order's
-- deleted_at, so that the listing and the expansion of the same provider could
-- not disagree about which lines exist. With the column gone (ADR 0054) the
-- join has nothing left to eliminate — order_id is NOT NULL and REFERENCES
-- orders, so every line has exactly one order and an inner join can never drop
-- a row. A join that cannot change the answer is cost that reads like a rule,
-- so it is gone with the condition it carried.
-- name: GetOrderLineItemsByIDs :many
SELECT * FROM order_line_items
WHERE id = ANY (sqlc.arg('ids')::text[])
ORDER BY id;
