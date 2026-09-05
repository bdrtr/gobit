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
