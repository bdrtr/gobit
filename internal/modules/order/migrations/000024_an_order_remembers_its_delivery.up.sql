-- An order remembers the delivery it was sold (ADR 0198).
--
-- The order kept the shipping TOTAL its cart computed and dropped the methods
-- behind it, so an operator opening a parcel could not see whether the shopper
-- paid for the standard or the next-day service. These rows are the cart's
-- shipping methods as the checkout priced them, written in the order's own
-- transaction and never changed afterwards, as the order's lines are.
--
-- shipping_option_id is the fulfillment module's id and is NOT a foreign key
-- (Principle 2.2); it is NULL where the cart's method named no option. The
-- method's free-form data stays on the cart: it can hold what the shopper typed,
-- and the order does not need it to know which service was sold.
CREATE TABLE IF NOT EXISTS order_shipping_methods (
    id                 TEXT        PRIMARY KEY,
    order_id           TEXT        NOT NULL REFERENCES orders (id),
    shipping_option_id TEXT,
    name               TEXT        NOT NULL,
    amount             BIGINT      NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT order_shipping_methods_amount_nonneg CHECK (amount >= 0),
    CONSTRAINT order_shipping_methods_name_not_blank CHECK (length(btrim(name)) > 0)
);

CREATE INDEX IF NOT EXISTS order_shipping_methods_order_idx
    ON order_shipping_methods (order_id, created_at, id);
