-- A delivery can be changed before it ships (ADR 0199).
--
-- A change does not edit the shipping method the order was sold. It is a row
-- beside it naming the method it replaces and the option, name and amount the
-- fulfillment module quoted for the new service; the method's current delivery
-- is its latest change. The difference is the new amount less the one it
-- replaces.
--
-- A change that costs less writes a credit line for the difference in the same
-- transaction, and the row names it: the two imply each other, which the CHECK
-- below holds. A change that costs MORE is not written at all yet — there is no
-- way to take the difference, and a row waiting for money nothing can collect
-- is a state nothing could leave (000008's lesson). ADR 0199's CHECK holds that
-- too, and the record that funds an upgrade will widen it.
CREATE TABLE IF NOT EXISTS order_delivery_changes (
    id                 TEXT        PRIMARY KEY,
    order_id           TEXT        NOT NULL REFERENCES orders (id),
    shipping_method_id TEXT        NOT NULL REFERENCES order_shipping_methods (id),
    shipping_option_id TEXT        NOT NULL,
    name               TEXT        NOT NULL,
    amount             BIGINT      NOT NULL,
    difference         BIGINT      NOT NULL,
    credit_line_id     TEXT        REFERENCES order_credit_lines (id),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT order_delivery_changes_amount_nonneg CHECK (amount >= 0),
    CONSTRAINT order_delivery_changes_name_not_blank CHECK (length(btrim(name)) > 0),
    CONSTRAINT order_delivery_changes_option_not_blank CHECK (length(btrim(shipping_option_id)) > 0),
    CONSTRAINT order_delivery_changes_costs_no_more CHECK (difference <= 0),
    -- A credit line exactly when the change costs less.
    CONSTRAINT order_delivery_changes_credit_when_cheaper
        CHECK ((difference < 0) = (credit_line_id IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS order_delivery_changes_order_idx
    ON order_delivery_changes (order_id, created_at, id);

-- A credit line is written off by one change at most, and the order journal
-- reads a credit line's change by this index.
CREATE UNIQUE INDEX IF NOT EXISTS order_delivery_changes_credit_line_uniq
    ON order_delivery_changes (credit_line_id) WHERE credit_line_id IS NOT NULL;
