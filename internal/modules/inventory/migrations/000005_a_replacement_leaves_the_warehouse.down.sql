-- The rollback narrows the vocabulary again, and it can only do so where no row
-- uses the widened half.
--
-- The order matters: the movement constraints come back to their 000004 shape
-- BEFORE the column goes, because the equivalence being restored names 'sale'
-- alone and a 'replacement' row would violate it. A rollback with such a row in
-- the table FAILS, and that is the correct outcome — the alternative is a
-- migration that deletes a ledger entry to make itself succeed.
ALTER TABLE inventory_movements
    DROP CONSTRAINT IF EXISTS inventory_movements_promise_names_its_reservation;

ALTER TABLE inventory_movements
    DROP CONSTRAINT IF EXISTS inventory_movements_replacement_deducts;

ALTER TABLE inventory_movements
    DROP CONSTRAINT IF EXISTS inventory_movements_reason_valid;

ALTER TABLE inventory_movements
    ADD CONSTRAINT inventory_movements_reason_valid
        CHECK (reason IN ('stock_count', 'adjustment', 'sale', 'return_restock'));

ALTER TABLE inventory_movements
    ADD CONSTRAINT inventory_movements_sale_names_its_reservation
        CHECK ((reason = 'sale') = (reservation_id IS NOT NULL));

ALTER TABLE inventory_reservations
    DROP CONSTRAINT IF EXISTS inventory_reservations_purpose_valid;

ALTER TABLE inventory_reservations
    DROP COLUMN IF EXISTS purpose;
