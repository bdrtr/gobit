-- order_line_cancellations records units of a line that will NOT be delivered.
--
-- # What it is for
--
-- Until now a cancellation was all or nothing: `CancelOrder` refuses an order
-- that has collected money and takes the whole order with it. The ordinary case
-- it could not express is one line of a live order going out of stock, being
-- damaged in the warehouse, or being dropped at the customer's request while the
-- rest of the order ships.
--
-- # Why the units are a ROW and not a column on the line
--
-- `order_line_items.quantity` is the cart's snapshot: how many were bought. The
-- order is 000001's "permanent answer to the question what was sold at that
-- moment", and lowering the quantity would erase the sale rather than record the
-- cancellation -- and would break orders_totals_consistent, because the line's
-- subtotal is pinned to unit_price x quantity.
--
-- Rows also carry what a column cannot: a cancellation has a reason and a
-- moment, two units canceled in two acts are two facts, and the running total
-- is their SUM. That is the same answer 000014 gave for credits and 000001 gave
-- for the outstanding amount: a derived value is read, not stored.
--
-- # Why there is no order_id column
--
-- The line already names its order. A second copy could disagree with the first,
-- and the listing that needs it joins one row.
--
-- # What it does NOT do
--
-- It does not move money and it does not change the order's status. A canceled
-- unit that was already paid for is a refund or a credit (000014), which is a
-- separate act with a separate authorization; and an order whose every line is
-- canceled is still an order somebody has to close.
CREATE TABLE IF NOT EXISTS order_line_cancellations (
    id                 TEXT        PRIMARY KEY,
    order_line_item_id TEXT        NOT NULL REFERENCES order_line_items (id) ON DELETE CASCADE,
    quantity           BIGINT      NOT NULL,
    -- reason is the merchant's short word for WHY, from their own vocabulary;
    -- this module does not enumerate it. What it refuses is an unexplained one.
    reason             TEXT        NOT NULL,
    note               TEXT        NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT order_line_cancellations_quantity_positive CHECK (quantity > 0),
    CONSTRAINT order_line_cancellations_reason_present    CHECK (reason <> '')
);

-- The ceiling -- bought minus returned minus already canceled -- spans rows and
-- cannot be a CHECK; the service reads this index under the order's lock.
CREATE INDEX IF NOT EXISTS order_line_cancellations_line_idx
    ON order_line_cancellations (order_line_item_id, created_at, id);
