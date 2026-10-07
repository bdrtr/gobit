-- An exchange names the return that takes its goods back, and each item it
-- sends is priced when it is written (ADR 0432).
--
-- order_return_id is the return whose lines are the goods coming back. The key
-- is composite, as an add-on's parent is (migration 000031), so an exchange can
-- only name a return of its own order. One return answers one live exchange at
-- most; a canceled exchange lets its return go. An exchange written without one
-- keeps the difference the operator typed (ADR 0120).
--
-- A replacement item of such an exchange carries what it is sold at:
-- unit_price is the unit price it was priced at, in the order's convention
-- (tax included when the order's prices include it, ADR 0246); total is what
-- the buyer pays for the item's units, tax included; tax_total is the tax in
-- it, at tax_rate_bps, and tax_components is the per-rate breakdown when a
-- stack taxed it (ADR 0095). priced_by says where the price came from: the
-- order line's own sold figures for an item naming a line, a quote in the
-- order's region, channel and customer or the operator's price for an item
-- naming a variant. A claim's replacement and an exchange written without a
-- return are not priced, and the columns stay NULL together.
ALTER TABLE order_returns
    ADD CONSTRAINT order_returns_order_id_id_uniq UNIQUE (order_id, id);

ALTER TABLE order_exchanges
    ADD COLUMN IF NOT EXISTS order_return_id TEXT NULL;

ALTER TABLE order_exchanges
    ADD CONSTRAINT order_exchanges_return_fk
        FOREIGN KEY (order_id, order_return_id) REFERENCES order_returns (order_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS order_exchanges_return_uniq
    ON order_exchanges (order_return_id)
    WHERE order_return_id IS NOT NULL AND status <> 'canceled';

ALTER TABLE order_replacement_items
    ADD COLUMN IF NOT EXISTS unit_price     BIGINT,
    ADD COLUMN IF NOT EXISTS total          BIGINT,
    ADD COLUMN IF NOT EXISTS tax_total      BIGINT,
    ADD COLUMN IF NOT EXISTS tax_rate_bps   INTEGER,
    ADD COLUMN IF NOT EXISTS tax_components JSONB,
    ADD COLUMN IF NOT EXISTS priced_by      TEXT;

-- Each comparison tests its columns for NULL before comparing them, so every
-- CHECK answers TRUE or FALSE (ADR 0169).
ALTER TABLE order_replacement_items
    ADD CONSTRAINT order_replacement_items_priced_together
        CHECK ((unit_price IS NULL) = (priced_by IS NULL)
           AND (unit_price IS NULL) = (total IS NULL)
           AND (unit_price IS NULL) = (tax_total IS NULL)
           AND (unit_price IS NULL) = (tax_rate_bps IS NULL)
           AND (tax_components IS NULL OR unit_price IS NOT NULL)),
    ADD CONSTRAINT order_replacement_items_priced_by_known
        CHECK (priced_by IS NULL OR priced_by IN ('line', 'quote', 'operator')),
    ADD CONSTRAINT order_replacement_items_priced_by_shape
        CHECK (priced_by IS NULL OR (priced_by = 'line') = (order_line_item_id IS NOT NULL)),
    ADD CONSTRAINT order_replacement_items_price_range
        CHECK (unit_price IS NULL OR total IS NULL OR tax_total IS NULL OR tax_rate_bps IS NULL
               OR (unit_price >= 0 AND total >= 0 AND tax_total >= 0 AND tax_total <= total
                   AND tax_rate_bps >= 0 AND tax_rate_bps <= 10000)),
    ADD CONSTRAINT order_replacement_items_tax_components_list
        CHECK (tax_components IS NULL OR jsonb_typeof(tax_components) = 'array');
