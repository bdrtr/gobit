-- An order line can be an add-on of another line of the same order (ADR 0229):
-- the engraving sold for one ring, the wrap for one mug. The parent is named at
-- insert, since a line is never updated, and the key is composite so that a
-- line can only belong to a line of its own order.
ALTER TABLE order_line_items
    ADD CONSTRAINT order_line_items_order_id_id_uniq UNIQUE (order_id, id);

ALTER TABLE order_line_items
    ADD COLUMN IF NOT EXISTS parent_line_item_id TEXT NULL;

ALTER TABLE order_line_items
    ADD CONSTRAINT order_line_items_parent_fk
        FOREIGN KEY (order_id, parent_line_item_id) REFERENCES order_line_items (order_id, id);

ALTER TABLE order_line_items
    ADD CONSTRAINT order_line_items_parent_not_itself
        CHECK (parent_line_item_id IS NULL OR parent_line_item_id <> id);

CREATE INDEX IF NOT EXISTS order_line_items_parent_idx
    ON order_line_items (parent_line_item_id) WHERE parent_line_item_id IS NOT NULL;
