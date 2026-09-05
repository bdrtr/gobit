-- Indexes that make the order LINE readable as its own entity.
--
-- The Query layer now offers "order_line_item" next to "order", because the
-- order entity cannot answer the question demand analytics starts from: WHICH
-- VARIANTS SOLD IN A PERIOD. That question filters lines by a DATE, and the
-- date a line was sold is not on the line -- it is the order's placed_at.
--
-- # Why the date stays on the order and is NOT copied onto the line
--
-- A placed_at column on order_line_items would turn the listing into a single
-- table index range scan, which is the cheaper shape. It was not taken:
--
--  1. It would be a SECOND source of truth for one fact. The line is written in
--     the same transaction as the order, so the copy would be correct at birth,
--     but NOTHING IN THE SCHEMA would keep it correct afterwards. An exchange
--     adds lines to an order that already exists (plan Section 6); such a line
--     would carry the moment the exchange was made, and a report drawn from it
--     would disagree with the order it belongs to without anything failing.
--  2. It would need a backfill, and a backfilled value cannot be told apart
--     from a written one later on. That is the trap 000004 documents for
--     tax_rate_bps and accepts there because the rate CANNOT be recovered from
--     the row. Here it can: the order still holds the date.
--
-- The join's price is one index lookup per order in the period, over
-- order_line_items_order_idx, which the line table already carries for the
-- order detail read. The copy's price is a fact that can drift. The choice is
-- the join.
--
-- # The query shape these indexes serve
--
-- queries/order_line_items.sql, ListOrderLineItemsFiltered:
--
--   SELECT li.* FROM order_line_items li JOIN orders o ON o.id = li.order_id
--   WHERE li.deleted_at IS NULL AND o.deleted_at IS NULL
--     AND o.placed_at >= $from AND o.placed_at < $to
--     [AND li.order_id = $order] [AND li.variant_id = $variant]
--   ORDER BY o.placed_at DESC, li.id DESC
--   LIMIT $n OFFSET $m;

-- orders_placed_at_idx is what makes the date range usable at all. Without it
-- the range is a sequential scan of every order ever placed: the cost of asking
-- for LAST MONTH would grow with the whole sales history instead of with the
-- month. The descending order matches the ORDER BY, so the LIMIT stops the scan
-- early rather than sorting the entire period first.
--
-- It is NOT a duplicate of orders_alive_idx (created_at DESC, id DESC). The two
-- columns hold the same instant today -- CreateOrder writes placed_at = now()
-- in the same statement that defaults created_at -- but they MEAN different
-- things: created_at is when the row was written, placed_at is when the sale
-- happened, and an order imported from another system would separate them. The
-- planner cannot use an index on one column for a predicate on the other, equal
-- values or not, so the filter that means "sold in this period" needs its own
-- index.
CREATE INDEX IF NOT EXISTS orders_placed_at_idx
    ON orders (placed_at DESC, id DESC)
    WHERE deleted_at IS NULL;

-- order_line_items_variant_idx serves the other half of the same question:
-- "how much of THIS variant was sold". Without it the variant filter is a full
-- scan of the line table, which is the module's largest -- it grows with every
-- line of every order, not with the number of orders.
--
-- order_id is the second column so that the join key comes out of the INDEX
-- itself: when the variant is selective but the period excludes most of its
-- lines, the discarded rows never have to be read from the heap. This is a
-- reasoned choice about the plan shape, not a measured one; the module has no
-- benchmark of the line table's size at the time of writing.
CREATE INDEX IF NOT EXISTS order_line_items_variant_idx
    ON order_line_items (variant_id, order_id)
    WHERE deleted_at IS NULL;
