ALTER TABLE order_line_items DROP CONSTRAINT IF EXISTS order_line_items_components_is_array;
ALTER TABLE order_line_items DROP COLUMN IF EXISTS components;
