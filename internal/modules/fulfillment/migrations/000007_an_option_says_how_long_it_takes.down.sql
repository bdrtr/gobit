ALTER TABLE shipping_options
    DROP CONSTRAINT IF EXISTS shipping_options_delivery_days_range,
    DROP CONSTRAINT IF EXISTS shipping_options_delivery_days_paired;

ALTER TABLE shipping_options
    DROP COLUMN IF EXISTS delivery_max_days,
    DROP COLUMN IF EXISTS delivery_min_days;
