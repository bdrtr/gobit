-- A row written under the order's lock is stamped when it is written
-- (ADR 0241, D164).
--
-- now() is the moment the transaction BEGAN. A delivery change and an address
-- correction take the order's lock before they write, so one that began first
-- and waited was written after the one that held the lock and stamped before
-- it. The delivery changes are read in this column's order and the last one is
-- the delivery, which took the earlier decision for the later; the address that
-- waited was superseded before it had been written, which its CHECK refused.
-- clock_timestamp() is the moment of the write, which the lock puts in the order
-- the rows were written. The rows already there keep their stamps.
ALTER TABLE order_delivery_changes
    ALTER COLUMN created_at SET DEFAULT clock_timestamp();
ALTER TABLE order_addresses
    ALTER COLUMN created_at SET DEFAULT clock_timestamp();
