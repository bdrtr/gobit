-- An order line keeps what the shopper wrote on the cart line it came from
-- (ADR 0223): the engraving is what was sold, and the shop reads it to make it.
ALTER TABLE order_line_items
    ADD COLUMN IF NOT EXISTS properties JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE order_line_items
    ADD CONSTRAINT order_line_items_properties_is_object CHECK (jsonb_typeof(properties) = 'object');
