ALTER TABLE order_line_items DROP CONSTRAINT IF EXISTS order_line_items_properties_is_object;
ALTER TABLE order_line_items DROP COLUMN IF EXISTS properties;
