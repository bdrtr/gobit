DROP INDEX IF EXISTS orders_placed_by_idx;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_placed_by_not_blank;
ALTER TABLE orders DROP COLUMN IF EXISTS placed_by;
