-- order_line_taxes queries.
--
-- A line's tax breakdown is written ONCE, with the line, and is never updated:
-- it is part of the same permanent answer the line is, so there is no UPDATE
-- query here for the same reason order_line_items.sql has none.

-- name: CreateOrderLineTax :one
INSERT INTO order_line_taxes (
    id, order_line_item_id, position, rate_id, rate_bps,
    compound, taxable_amount, tax_amount
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- ListOrderLineTaxesByOrder reads every component of every line of ONE order.
--
-- It is keyed by the ORDER rather than by the line so that reading an order
-- stays two queries whatever the line count: asking per line would put the
-- module's only N+1 on its most-read path.
--
-- The ORDER BY is the position within the line, because a compound component's
-- base is everything below it and a breakdown read out of order cannot be
-- reproduced.

-- name: ListOrderLineTaxesByOrder :many
SELECT t.* FROM order_line_taxes t
JOIN order_line_items li ON li.id = t.order_line_item_id
WHERE li.order_id = $1
ORDER BY t.order_line_item_id, t.position;
