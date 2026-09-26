-- Rolling back puts 000025's rule back, and a database holding a paid change
-- refuses it: the CHECK below fails on the row rather than dropping which
-- collection paid for it.
DROP INDEX IF EXISTS order_delivery_changes_dearer_idx;
DROP INDEX IF EXISTS order_delivery_changes_payment_collection_uniq;
ALTER TABLE order_delivery_changes
    DROP CONSTRAINT IF EXISTS order_delivery_changes_paid_when_dearer;
ALTER TABLE order_delivery_changes
    ADD CONSTRAINT order_delivery_changes_costs_no_more CHECK (difference <= 0);
ALTER TABLE order_delivery_changes DROP COLUMN IF EXISTS payment_collection_id;
