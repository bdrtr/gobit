ALTER TABLE order_shipping_methods DROP COLUMN IF EXISTS seq;
ALTER TABLE order_line_cancellations DROP COLUMN IF EXISTS seq;
ALTER TABLE order_replacement_items DROP COLUMN IF EXISTS seq;
ALTER TABLE order_return_items DROP COLUMN IF EXISTS seq;
