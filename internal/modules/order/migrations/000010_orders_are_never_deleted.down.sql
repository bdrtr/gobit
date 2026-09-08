-- Rolling 000010 back: the six columns and the fifteen partial indexes return,
-- under the names 000001, 000002 and 000006 gave them.
--
-- The rows come back with deleted_at NULL, the value every row had while the
-- columns existed -- nothing ever wrote them, so nothing is lost by restoring
-- them empty.
--
-- The indexes are dropped and rebuilt rather than left alone. They exist here
-- with WIDER predicates than the originals (the two uniqueness rules now cover
-- every row, because no row can be retired), and a down that left them would
-- leave the schema one step short of where 000001 put it while looking
-- finished. The next up would then skip them because of IF NOT EXISTS and the
-- difference would stand for ever.
ALTER TABLE orders             ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
ALTER TABLE order_line_items   ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
ALTER TABLE order_returns      ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
ALTER TABLE order_return_items ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
ALTER TABLE order_claims       ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
ALTER TABLE order_exchanges    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

DROP INDEX IF EXISTS orders_idempotency_key_uniq;
DROP INDEX IF EXISTS orders_listing_idx;
DROP INDEX IF EXISTS orders_customer_idx;
DROP INDEX IF EXISTS orders_region_idx;
DROP INDEX IF EXISTS orders_status_idx;
DROP INDEX IF EXISTS orders_cart_idx;
DROP INDEX IF EXISTS orders_placed_at_idx;
DROP INDEX IF EXISTS order_line_items_order_idx;
DROP INDEX IF EXISTS order_line_items_variant_idx;
DROP INDEX IF EXISTS order_returns_order_idx;
DROP INDEX IF EXISTS order_exchanges_order_idx;
DROP INDEX IF EXISTS order_claims_order_idx;
DROP INDEX IF EXISTS order_return_items_line_uniq;
DROP INDEX IF EXISTS order_return_items_return_idx;
DROP INDEX IF EXISTS order_return_items_line_idx;

CREATE UNIQUE INDEX IF NOT EXISTS orders_idempotency_key_uniq
    ON orders (idempotency_key)
    WHERE idempotency_key IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS orders_alive_idx
    ON orders (created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS orders_customer_idx
    ON orders (customer_id)
    WHERE customer_id IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS orders_region_idx
    ON orders (region_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS orders_status_idx
    ON orders (status)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS orders_cart_idx
    ON orders (cart_id)
    WHERE cart_id IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS orders_placed_at_idx
    ON orders (placed_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS order_line_items_order_idx
    ON order_line_items (order_id, created_at, id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS order_line_items_variant_idx
    ON order_line_items (variant_id, order_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS order_returns_order_idx
    ON order_returns (order_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS order_exchanges_order_idx
    ON order_exchanges (order_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS order_claims_order_idx
    ON order_claims (order_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS order_return_items_line_uniq
    ON order_return_items (order_return_id, order_line_item_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS order_return_items_return_idx
    ON order_return_items (order_return_id, created_at, id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS order_return_items_line_idx
    ON order_return_items (order_line_item_id)
    WHERE deleted_at IS NULL;
