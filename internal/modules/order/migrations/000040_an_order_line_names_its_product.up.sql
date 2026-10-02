-- An order line names the product it sold as well as its variant (ADR 0365):
-- the line's title is the variant's ("1 kg / Filtre"), and the order kept no
-- record of which product that was once the catalog moved on. The checkout
-- copies the product's title at the moment of sale, as it copies the
-- variant's. A line written before is the empty string, which a reader takes
-- for "not recorded".
ALTER TABLE order_line_items
    ADD COLUMN IF NOT EXISTS product_title TEXT NOT NULL DEFAULT '';
