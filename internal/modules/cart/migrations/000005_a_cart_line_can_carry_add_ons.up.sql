-- A cart line can carry add-on lines (ADR 0229): an engraving or a gift wrap
-- is a variant with its own price, opened as a line bound to the line it
-- modifies.
--
-- parent_line_id names the line an add-on belongs to, in the same cart (the key
-- is composite for that). add_on_key is, on a line standing on its own, a
-- digest of the add-ons it carries, and empty on an add-on: the same ring with
-- an engraving and the same ring without one are two lines, so the set of
-- add-ons is part of what a line IS, as its properties are (ADR 0223). The
-- identity index keys on both.
ALTER TABLE cart_line_items
    ADD CONSTRAINT cart_line_items_cart_id_id_uniq UNIQUE (cart_id, id);

ALTER TABLE cart_line_items
    ADD COLUMN IF NOT EXISTS parent_line_id TEXT NULL,
    ADD COLUMN IF NOT EXISTS add_on_key TEXT NOT NULL DEFAULT '';

ALTER TABLE cart_line_items
    ADD CONSTRAINT cart_line_items_parent_fk
        FOREIGN KEY (cart_id, parent_line_id) REFERENCES cart_line_items (cart_id, id);

ALTER TABLE cart_line_items
    ADD CONSTRAINT cart_line_items_parent_not_itself
        CHECK (parent_line_id IS NULL OR parent_line_id <> id);

-- An add-on carries no add-ons of its own.
ALTER TABLE cart_line_items
    ADD CONSTRAINT cart_line_items_add_on_key_on_roots
        CHECK (parent_line_id IS NULL OR add_on_key = '');

DROP INDEX IF EXISTS cart_line_items_cart_variant_properties_uniq;

CREATE UNIQUE INDEX IF NOT EXISTS cart_line_items_identity_uniq
    ON cart_line_items (cart_id, variant_id, md5(properties::text), coalesce(parent_line_id, ''), add_on_key)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS cart_line_items_parent_idx
    ON cart_line_items (parent_line_id) WHERE parent_line_id IS NOT NULL AND deleted_at IS NULL;
