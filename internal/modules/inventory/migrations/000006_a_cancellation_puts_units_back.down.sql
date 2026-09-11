-- Forgetting what a movement was for, and that a cancellation is a reason.
--
-- Rows whose reason is 'cancellation' would violate the narrowed CHECK, so they
-- are rewritten as adjustments first: the arithmetic they did is still true and
-- the fact they recorded is what is lost.
DROP INDEX IF EXISTS inventory_movements_sale_reference_idx;
DROP INDEX IF EXISTS inventory_movements_cancellation_once_idx;

ALTER TABLE inventory_movements
    DROP CONSTRAINT IF EXISTS inventory_movements_cancellation_has_reference;
ALTER TABLE inventory_movements
    DROP CONSTRAINT IF EXISTS inventory_movements_reference_not_blank;
ALTER TABLE inventory_movements
    DROP CONSTRAINT IF EXISTS inventory_movements_cancellation_is_positive;

UPDATE inventory_movements SET reason = 'adjustment' WHERE reason = 'cancellation';

ALTER TABLE inventory_movements
    DROP COLUMN IF EXISTS reference;

ALTER TABLE inventory_movements
    DROP CONSTRAINT IF EXISTS inventory_movements_reason_valid;
ALTER TABLE inventory_movements
    ADD CONSTRAINT inventory_movements_reason_valid
    CHECK (reason IN ('stock_count', 'adjustment', 'sale', 'return_restock', 'replacement'));
