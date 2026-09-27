DROP INDEX IF EXISTS customer_wishlist_item_alert_idx;

ALTER TABLE customer_wishlist_item
    DROP CONSTRAINT IF EXISTS customer_wishlist_item_alert_marked,
    DROP COLUMN IF EXISTS stock_alert_armed_at,
    DROP COLUMN IF EXISTS stock_alert_channels,
    DROP COLUMN IF EXISTS stock_alert;
