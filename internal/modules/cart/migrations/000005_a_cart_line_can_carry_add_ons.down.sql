-- Rolling back drops the add-on bond, which a cart holding an add-on line, or
-- one variant with the same words on two lines told apart by their add-ons,
-- cannot survive; the rollback refuses rather than choose which lines to lose.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM cart_line_items
        WHERE deleted_at IS NULL AND (parent_line_id IS NOT NULL OR add_on_key <> '')
    ) THEN
        RAISE EXCEPTION 'a cart holds add-on lines; rolling back ADR 0229 would have to drop them';
    END IF;
END
$$;

DROP INDEX IF EXISTS cart_line_items_parent_idx;
DROP INDEX IF EXISTS cart_line_items_identity_uniq;

CREATE UNIQUE INDEX IF NOT EXISTS cart_line_items_cart_variant_properties_uniq
    ON cart_line_items (cart_id, variant_id, md5(properties::text))
    WHERE deleted_at IS NULL;

ALTER TABLE cart_line_items DROP CONSTRAINT IF EXISTS cart_line_items_add_on_key_on_roots;
ALTER TABLE cart_line_items DROP CONSTRAINT IF EXISTS cart_line_items_parent_not_itself;
ALTER TABLE cart_line_items DROP CONSTRAINT IF EXISTS cart_line_items_parent_fk;
ALTER TABLE cart_line_items DROP COLUMN IF EXISTS add_on_key;
ALTER TABLE cart_line_items DROP COLUMN IF EXISTS parent_line_id;
ALTER TABLE cart_line_items DROP CONSTRAINT IF EXISTS cart_line_items_cart_id_id_uniq;
