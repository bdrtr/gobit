-- Rolling back drops which return an exchange takes back and what its goods
-- were priced at, and a database where a live exchange names a return refuses
-- it: the CHECK below fails on that row first, before anything is dropped.
-- Without the column the return could be refunded beside the exchange whose
-- difference already counts its goods, and the money would go back twice. An
-- exchange withdrawn first lets the rollback through, and its replacements'
-- prices go with the columns.
ALTER TABLE order_exchanges
    ADD CONSTRAINT order_exchanges_names_no_live_return
        CHECK (order_return_id IS NULL OR status = 'canceled');
ALTER TABLE order_exchanges DROP CONSTRAINT IF EXISTS order_exchanges_names_no_live_return;

ALTER TABLE order_replacement_items
    DROP CONSTRAINT IF EXISTS order_replacement_items_tax_components_list,
    DROP CONSTRAINT IF EXISTS order_replacement_items_price_range,
    DROP CONSTRAINT IF EXISTS order_replacement_items_priced_by_shape,
    DROP CONSTRAINT IF EXISTS order_replacement_items_priced_by_known,
    DROP CONSTRAINT IF EXISTS order_replacement_items_priced_together;
ALTER TABLE order_replacement_items
    DROP COLUMN IF EXISTS priced_by,
    DROP COLUMN IF EXISTS tax_components,
    DROP COLUMN IF EXISTS tax_rate_bps,
    DROP COLUMN IF EXISTS tax_total,
    DROP COLUMN IF EXISTS total,
    DROP COLUMN IF EXISTS unit_price;
DROP INDEX IF EXISTS order_exchanges_return_uniq;
ALTER TABLE order_exchanges DROP CONSTRAINT IF EXISTS order_exchanges_return_fk;
ALTER TABLE order_exchanges DROP COLUMN IF EXISTS order_return_id;
ALTER TABLE order_returns DROP CONSTRAINT IF EXISTS order_returns_order_id_id_uniq;
