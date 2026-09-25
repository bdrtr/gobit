-- The order journal reads by time (ADR 0188).
--
-- The order's side of the books derives its entries, when it is asked, from
-- the orders placed and canceled inside a window and the credit lines written
-- in it. None of those moments had an index, so every read would scan every
-- order the installation ever took. The cancellation index is PARTIAL: most
-- orders are never canceled, and those rows are never an answer.
CREATE INDEX IF NOT EXISTS orders_placed_at_idx
    ON orders (placed_at, id);

CREATE INDEX IF NOT EXISTS orders_canceled_at_idx
    ON orders (canceled_at, id)
    WHERE canceled_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS order_credit_lines_created_at_idx
    ON order_credit_lines (created_at, id);
