-- The reverse of 000009, in the reverse order.
--
-- Rows whose reason is 'supplier_receipt' would violate the narrowed CHECK, so
-- they are rewritten as adjustments with no reference first, as 000006's
-- reverse does with a cancellation: the arithmetic they did is still true and
-- the fact they recorded is what is lost.
DROP INDEX IF EXISTS inventory_movements_supplier_receipt_once_idx;
ALTER TABLE inventory_movements
    DROP CONSTRAINT IF EXISTS inventory_movements_supplier_receipt_names_its_receipt;
ALTER TABLE inventory_movements
    DROP CONSTRAINT IF EXISTS inventory_movements_supplier_receipt_adds;

UPDATE inventory_movements SET reason = 'adjustment', reference = NULL
WHERE reason = 'supplier_receipt';

ALTER TABLE inventory_movements
    DROP CONSTRAINT IF EXISTS inventory_movements_reason_valid;
ALTER TABLE inventory_movements
    ADD CONSTRAINT inventory_movements_reason_valid
    CHECK (reason IN ('stock_count', 'adjustment', 'sale', 'return_restock',
                      'replacement', 'cancellation'));

DROP TABLE IF EXISTS inventory_supplier_receipts;
