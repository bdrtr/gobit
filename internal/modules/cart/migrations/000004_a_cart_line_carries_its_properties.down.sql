-- Rolling back puts one line per variant back, which a cart holding the same
-- variant twice with different words cannot satisfy; the rollback refuses
-- rather than choose which words to lose.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM cart_line_items
        WHERE deleted_at IS NULL
        GROUP BY cart_id, variant_id
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'a cart holds one variant on two lines with different properties; '
            'rolling back ADR 0223 would have to drop one';
    END IF;
END
$$;

DROP INDEX IF EXISTS cart_line_items_cart_variant_properties_uniq;

CREATE UNIQUE INDEX IF NOT EXISTS cart_line_items_cart_variant_uniq
    ON cart_line_items (cart_id, variant_id)
    WHERE deleted_at IS NULL;

ALTER TABLE cart_line_items DROP CONSTRAINT IF EXISTS cart_line_items_properties_is_object;
ALTER TABLE cart_line_items DROP COLUMN IF EXISTS properties;
