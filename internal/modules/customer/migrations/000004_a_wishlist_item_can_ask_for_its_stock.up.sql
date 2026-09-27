-- A wishlist item can ask to be told when its variant is back in stock
-- (ADR 0215).
--
-- The mark is the customer's own, set by the proven customer on their own list,
-- and the mail goes to the customer's own address: nothing here names anybody
-- else (ADR 0051's CONFINED). The channels are the ones the storefront request
-- carried when the mark was set, because whether a variant is in stock is
-- answered over the warehouses those channels serve; NULL is a mark set with no
-- channel, which reads the whole catalog.
--
-- armed_at is set the first time the variant is seen OUT of stock after the
-- mark, so a mark set on a variant that is in stock waits for it to run out and
-- come back rather than mailing at once. A mail clears the mark: it is sent once.
ALTER TABLE customer_wishlist_item
    ADD COLUMN IF NOT EXISTS stock_alert          BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS stock_alert_channels TEXT[],
    ADD COLUMN IF NOT EXISTS stock_alert_armed_at TIMESTAMPTZ;

ALTER TABLE customer_wishlist_item
    ADD CONSTRAINT customer_wishlist_item_alert_marked
        CHECK (stock_alert OR (stock_alert_channels IS NULL AND stock_alert_armed_at IS NULL));

-- The alert job walks the marked items in key order.
CREATE INDEX IF NOT EXISTS customer_wishlist_item_alert_idx
    ON customer_wishlist_item (customer_id, variant_id)
    WHERE stock_alert;
