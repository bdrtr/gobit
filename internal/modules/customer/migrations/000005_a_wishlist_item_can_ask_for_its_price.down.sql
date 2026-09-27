DROP INDEX IF EXISTS customer_wishlist_item_alerts_idx;
CREATE INDEX IF NOT EXISTS customer_wishlist_item_alert_idx
    ON customer_wishlist_item (customer_id, variant_id)
    WHERE stock_alert;

ALTER TABLE customer_wishlist_item
    DROP CONSTRAINT IF EXISTS customer_wishlist_item_price_amount_nonneg,
    DROP CONSTRAINT IF EXISTS customer_wishlist_item_price_baseline_whole,
    DROP CONSTRAINT IF EXISTS customer_wishlist_item_price_unmarked_empty,
    DROP CONSTRAINT IF EXISTS customer_wishlist_item_price_marked,
    DROP COLUMN IF EXISTS price_alert_amount,
    DROP COLUMN IF EXISTS price_alert_currency,
    DROP COLUMN IF EXISTS price_alert_channels,
    DROP COLUMN IF EXISTS price_alert_region_id,
    DROP COLUMN IF EXISTS price_alert_marked_at,
    DROP COLUMN IF EXISTS price_alert;
