DROP INDEX IF EXISTS order_replacements_fulfillment_idx;
ALTER TABLE order_replacements DROP CONSTRAINT IF EXISTS order_replacements_recalls_nonneg;
ALTER TABLE order_replacements DROP COLUMN IF EXISTS recalls;
