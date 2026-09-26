ALTER TABLE carts DROP CONSTRAINT IF EXISTS carts_adds_to_order_not_blank;
ALTER TABLE carts DROP COLUMN IF EXISTS adds_to_order_id;
