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
WHERE order_id = $1 AND deleted_at IS NULL
ORDER BY created_at, id;

-- ListOrderLineItemsFiltered is the cross-module read of the LINE as an entity
-- of its own (the "order_line_item" Query provider).
--
-- # Why it joins orders when ListOrderLineItems does not
--
-- Two reasons, and both are about a fact the LINE DOES NOT HOLD.
--
-- The first is the date. This query's reason to exist is "which variants sold
-- in this period", and the moment a line was sold is the ORDER's placed_at, not
-- the line's created_at: created_at says when the row was written, and for a
-- line added later to an existing order (an exchange) the two are different
-- days. Filtering on the line's own stamp would answer a question nobody asked.
-- Why the date is not copied onto the line instead is argued in migration
-- 000006.
--
-- The second is liveness. ListOrderLineItems is always reached through an order
-- that was already found alive, so checking the line alone is enough there.
-- This query has no such caller: it is entered with a date or a variant and
-- must not report a line of a soft-deleted order as a sale. The order's
-- deleted_at is therefore part of the condition -- and it is part of
-- GetOrderLineItemsByIDs as well, so that the listing and the expansion of the
-- same provider cannot disagree about which lines exist. That divergence is the
-- exact failure 000001 warns about for order_summaries.
--
-- The ORDER BY is o.placed_at DESC, li.id DESC: the analytics reader wants the
-- most recent sales first, and li.id breaks the tie so a page boundary does not
-- move between two calls. orders_placed_at_idx (migration 000006) serves both
-- the range and the ordering.
-- name: ListOrderLineItemsFiltered :many
SELECT li.* FROM order_line_items li
    JOIN orders o ON o.id = li.order_id
WHERE li.deleted_at IS NULL
  AND o.deleted_at IS NULL
  AND (sqlc.narg('order_id')::text IS NULL OR li.order_id = sqlc.narg('order_id')::text)
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
-- It joins orders for the liveness reason ListOrderLineItemsFiltered gives: an
-- expansion that returned a line the listing hides would make the same entity
-- answer two different ways depending on which side of the query it was reached
-- from.
-- name: GetOrderLineItemsByIDs :many
SELECT li.* FROM order_line_items li
    JOIN orders o ON o.id = li.order_id
WHERE li.id = ANY (sqlc.arg('ids')::text[])
  AND li.deleted_at IS NULL
  AND o.deleted_at IS NULL
ORDER BY li.id;
