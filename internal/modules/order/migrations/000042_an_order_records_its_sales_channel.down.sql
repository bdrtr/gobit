DROP INDEX IF EXISTS orders_sales_channel_idx;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_sales_channel_not_blank;
ALTER TABLE orders DROP COLUMN IF EXISTS sales_channel_id;
