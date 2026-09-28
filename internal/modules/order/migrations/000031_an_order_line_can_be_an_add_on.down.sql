DROP INDEX IF EXISTS order_line_items_parent_idx;
ALTER TABLE order_line_items DROP CONSTRAINT IF EXISTS order_line_items_parent_not_itself;
ALTER TABLE order_line_items DROP CONSTRAINT IF EXISTS order_line_items_parent_fk;
ALTER TABLE order_line_items DROP COLUMN IF EXISTS parent_line_item_id;
ALTER TABLE order_line_items DROP CONSTRAINT IF EXISTS order_line_items_order_id_id_uniq;
