-- A wishlist item can ask to be told when its variant's price drops (ADR 0216).
--
-- The mark is the proven customer's own, as the stock alert is (ADR 0215). It
-- names the region the price is asked in, whose currency it is, and the sales
-- channels the request carried. The price at the mark is recorded by the first
-- pass of the alert job after it, as the currency and the amount the customer's
-- cart would be charged for one unit; marked_at names the mark, so a mark set
-- again is a new one.
ALTER TABLE customer_wishlist_item
    ADD COLUMN IF NOT EXISTS price_alert           BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS price_alert_marked_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS price_alert_region_id TEXT,
    ADD COLUMN IF NOT EXISTS price_alert_channels  TEXT[],
    ADD COLUMN IF NOT EXISTS price_alert_currency  TEXT,
    ADD COLUMN IF NOT EXISTS price_alert_amount    BIGINT;

ALTER TABLE customer_wishlist_item
    ADD CONSTRAINT customer_wishlist_item_price_marked
        CHECK (price_alert = (price_alert_marked_at IS NOT NULL AND price_alert_region_id IS NOT NULL)),
    ADD CONSTRAINT customer_wishlist_item_price_unmarked_empty
        CHECK (price_alert OR (price_alert_channels IS NULL AND price_alert_currency IS NULL
                               AND price_alert_amount IS NULL)),
    ADD CONSTRAINT customer_wishlist_item_price_baseline_whole
        CHECK ((price_alert_currency IS NULL) = (price_alert_amount IS NULL)),
    ADD CONSTRAINT customer_wishlist_item_price_amount_nonneg
        CHECK (price_alert_amount IS NULL OR price_alert_amount >= 0);

-- The alert job walks every marked item, of either kind, in key order.
DROP INDEX IF EXISTS customer_wishlist_item_alert_idx;
CREATE INDEX IF NOT EXISTS customer_wishlist_item_alerts_idx
    ON customer_wishlist_item (customer_id, variant_id)
    WHERE stock_alert OR price_alert;
