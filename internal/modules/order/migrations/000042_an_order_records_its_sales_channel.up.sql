-- An order records the sales channel its cart was opened in (ADR 0410), the
-- one channel the cart's prices and promotions were chosen in (ADR 0397); an
-- order from a cart that named none records none. The value is copied as the
-- cart held it, free text as placed_by is: this module does not read the auth
-- module's channels.
ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS sales_channel_id TEXT;
ALTER TABLE orders
    ADD CONSTRAINT orders_sales_channel_not_blank
        CHECK (sales_channel_id IS NULL OR length(btrim(sales_channel_id)) > 0);

-- The admin list's channel filter walks this index rather than every order,
-- in the list's own order; an order that recorded no channel is not in it.
CREATE INDEX IF NOT EXISTS orders_sales_channel_idx
    ON orders (sales_channel_id, created_at DESC, id DESC)
    WHERE sales_channel_id IS NOT NULL;
