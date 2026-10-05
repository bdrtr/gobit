ALTER TABLE order_line_items DROP CONSTRAINT IF EXISTS order_line_items_unit_cost_range;
ALTER TABLE order_line_items DROP COLUMN IF EXISTS unit_cost;
