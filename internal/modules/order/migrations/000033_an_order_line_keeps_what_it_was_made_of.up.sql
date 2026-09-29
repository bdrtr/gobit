-- An order line keeps the composition of the bundle it sold (ADR 0235): the
-- variants one unit of it held and how many of each, as they were at the sale.
-- A write-off, a canceled parcel and a return put these parts back, and the
-- bundle may have been edited since. An empty array is a line that is no
-- bundle.
ALTER TABLE order_line_items
    ADD COLUMN IF NOT EXISTS components JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE order_line_items
    ADD CONSTRAINT order_line_items_components_is_array CHECK (jsonb_typeof(components) = 'array');
