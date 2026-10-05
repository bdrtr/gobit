ALTER TABLE carts DROP CONSTRAINT IF EXISTS carts_sales_channel_id_not_blank;
ALTER TABLE carts DROP COLUMN IF EXISTS sales_channel_id;
