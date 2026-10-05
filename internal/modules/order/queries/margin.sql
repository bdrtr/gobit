-- Margin queries (ADR 0401).
--
-- PlacedMarginsOfOrders is the ONE definition of an order's placed margin: the
-- admin order read and the admin order list both ask it, a page at a time.
--
-- # What is summed
--
-- Every line that did not sell gift cards. A gift card line is a debt the shop
-- owes (ADR 0211), not goods sold, so it is neither sales nor cost. Exchange and
-- replacement goods live in their own tables and are outside it; an addition
-- is an order with a margin of its own.
--
-- Sales are a line's subtotal less its discount: the net base its tax was taken
-- from, in either market (ADR 0246), so the margin is net of tax. The cost is
-- the line's unit cost times its quantity, the quantity sold; a canceled unit,
-- a return and a credit do not move it.
--
-- # When there is no cost
--
-- A line without a cost (NULL) leaves the order with no cost and no margin,
-- never a cost of zero: a margin over the costed lines alone is not the order's.
-- A cost past the bound of an order's totals is no cost either, so the
-- difference the service takes stays inside an int64. lines_without_cost counts
-- the first case; it is zero in the second. The cost is summed as numeric, so a
-- sum past the bound is compared rather than overflowed, and comes back NULL
-- rather than a bigint that cannot say "none".
--
-- An order with no line that is not a gift card has no row.
-- order_line_items_order_idx serves the lookup.
-- name: PlacedMarginsOfOrders :many
SELECT li.order_id,
       SUM(li.subtotal - li.discount_total)::bigint AS sales,
       (COUNT(*) FILTER (WHERE li.unit_cost IS NULL))::bigint AS lines_without_cost,
       (CASE WHEN COUNT(*) FILTER (WHERE li.unit_cost IS NULL) = 0
                  AND SUM(li.unit_cost::numeric * li.quantity) <= sqlc.arg('cost_limit')::bigint
             THEN SUM(li.unit_cost::numeric * li.quantity) END)::numeric AS cost
FROM order_line_items li
WHERE li.order_id = ANY(sqlc.arg('order_ids')::text[]) AND NOT li.is_giftcard
GROUP BY li.order_id;
