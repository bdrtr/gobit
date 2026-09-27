-- A line records whether it sold a gift card (ADR 0211).
--
-- The flag is the product's at the moment of sale, copied as the title is. The
-- order's books hold a gift card's price as a debt to its holder rather than
-- as sales, and they are derived from this module's own rows; asking the
-- catalog at reading time would make a book depend on a product that may since
-- have been deleted.
--
-- It is false on every line written before it existed. No gift card was sold
-- through an order before ADR 0210, which is the record that made them.
ALTER TABLE order_line_items
    ADD COLUMN IF NOT EXISTS is_giftcard BOOLEAN NOT NULL DEFAULT false;
