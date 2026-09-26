DROP INDEX IF EXISTS orders_adds_to_order_idx;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_adds_to_another;
ALTER TABLE orders DROP COLUMN IF EXISTS adds_to_order_id;
