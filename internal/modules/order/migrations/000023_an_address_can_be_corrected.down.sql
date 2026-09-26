-- Rolling back keeps only the current rows' rule. A superseded row would break
-- the one-per-type index 000005 had, so a database that holds a correction
-- refuses this rollback rather than dropping what the order held before.
DROP INDEX IF EXISTS order_addresses_one_current_per_type;
CREATE UNIQUE INDEX IF NOT EXISTS order_addresses_one_per_type
    ON order_addresses (order_id, address_type);
ALTER TABLE order_addresses DROP CONSTRAINT IF EXISTS order_addresses_superseded_after_written;
ALTER TABLE order_addresses DROP COLUMN IF EXISTS superseded_at;
