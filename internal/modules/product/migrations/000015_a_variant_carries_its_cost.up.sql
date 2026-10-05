-- product_variant_cost: what one unit of a variant costs the shop, in one
-- currency, net of tax (ADR 0401).
--
-- A cost is one row per currency, as a price is, and gobit converts none. The
-- checkout copies the row in the order's currency onto the order line, so a row
-- written later changes no order already placed. The bounds are a price's
-- (pricing 000001): a whole amount in minor units from 0 to 10^12, so a cost
-- times a line's quantity stays inside an order's totals.
--
-- The rows are not part of the product's revision view (ADR 0221), as add-ons
-- are not, and a variant's soft deletion leaves them behind unread.
CREATE TABLE IF NOT EXISTS product_variant_cost (
    variant_id    text        NOT NULL REFERENCES product_variant (id) ON DELETE CASCADE,
    currency_code text        NOT NULL,
    amount        bigint      NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (variant_id, currency_code),
    CONSTRAINT product_variant_cost_currency_check CHECK (currency_code ~ '^[A-Z]{3}$'),
    CONSTRAINT product_variant_cost_amount_check CHECK (amount >= 0 AND amount <= 1000000000000)
);
