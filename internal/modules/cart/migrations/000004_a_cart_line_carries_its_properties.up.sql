-- A cart line carries what the shopper wrote on it (ADR 0223).
--
-- properties are the shopper's own words for one line: an engraving, a gift
-- message, a monogram. They are part of what the line IS: the same variant with
-- another engraving is another line, and the same variant with the same words
-- raises the quantity of the line already there. The unique index therefore
-- keys on them, through a hash of their canonical text: JSONB prints its keys
-- in one order whatever order they were written in, and a hash keeps the index
-- row small however long the words are.
ALTER TABLE cart_line_items
    ADD COLUMN IF NOT EXISTS properties JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE cart_line_items
    ADD CONSTRAINT cart_line_items_properties_is_object CHECK (jsonb_typeof(properties) = 'object');

DROP INDEX IF EXISTS cart_line_items_cart_variant_uniq;

CREATE UNIQUE INDEX IF NOT EXISTS cart_line_items_cart_variant_properties_uniq
    ON cart_line_items (cart_id, variant_id, md5(properties::text))
    WHERE deleted_at IS NULL;
