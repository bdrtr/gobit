-- The reverse of 000019, in the reverse order.
--
-- It fails LOUDLY if any variant-shaped row exists, and that is the correct
-- outcome rather than a bug: restoring NOT NULL on order_line_item_id while a row
-- has none cannot be done quietly, and a migration that deleted those rows would
-- be throwing away a promise somebody made to a customer. The operator's way out
-- is to decide what happens to each one.
DROP INDEX IF EXISTS order_replacement_items_variant_uniq;
DROP INDEX IF EXISTS order_replacement_items_line_uniq;

CREATE UNIQUE INDEX IF NOT EXISTS order_replacement_items_line_uniq
    ON order_replacement_items (order_replacement_id, order_line_item_id);

ALTER TABLE order_replacement_items
    DROP CONSTRAINT IF EXISTS order_replacement_items_variant_not_blank;
ALTER TABLE order_replacement_items
    DROP CONSTRAINT IF EXISTS order_replacement_items_names_one_thing;

ALTER TABLE order_replacement_items
    ALTER COLUMN order_line_item_id SET NOT NULL;

ALTER TABLE order_replacement_items
    DROP COLUMN IF EXISTS variant_id;
