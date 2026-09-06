-- inventory_reservations loses its deleted_at column.
--
-- # Why the column goes rather than gaining a writer
--
-- The table's own rule, written at the head of 000001 and repeated at the head
-- of its query file, is that A RESERVATION IS NEVER DELETED: the status moves
-- active -> released | confirmed, and the compensation step of the checkout saga
-- (ReleaseReservation) is idempotent ONLY because a released reservation can
-- still be read and told apart from one that never existed. A deleted_at on this
-- table is a second way of saying what status already says, and the two can
-- disagree — a reservation that is 'active' and deleted is a quantity promised
-- to a sale that no read can see and no release can give back.
--
-- The column was never written. Not once, since the module was created: every
-- read carried "deleted_at IS NULL", a predicate that has never been false, and
-- the audit built to catch exactly that could not see it until 2026-09-06,
-- because inventory_items and inventory_levels DO write a deleted_at and the
-- audit matched writes by bare column name (docs/gaps.md D16, D18).
--
-- The rejected alternative was to keep the column and write it — to make the
-- release a soft delete instead of a status change. That is the option the
-- schema comment in 000001 already argued against and it would break the saga:
-- "already released" and "never existed" would become the same answer, so a
-- retried compensation could not tell an idempotent second call from a bad id.
--
-- The order and payment modules hold ten deleted_at columns of the same shape
-- and they are recorded as a DECISION rather than removed (docs/gaps.md D9).
-- This one is not that decision: there the argument is that a money record must
-- be KEPT, which the column does not get in the way of. Here the column
-- contradicts a rule the table already enforces with its status machine.
--
-- # The indexes have to be rebuilt, and that is not optional
--
-- PostgreSQL drops any index whose PREDICATE names a dropped column, silently
-- and without a notice. Both of this table's indexes are partial on
-- deleted_at, so the DROP COLUMN below takes both with it; the CREATE INDEX
-- statements that follow are what keeps them. This was measured on a real
-- PostgreSQL 16 before the migration was written, on a probe table with a
-- UNIQUE partial index — the drop removed the index and the duplicate key that
-- had been impossible a statement earlier was accepted.
ALTER TABLE inventory_reservations DROP COLUMN IF EXISTS deleted_at;

-- The same two indexes as 000001, minus the predicate that named the column.
-- The 'active' half of the first one stays: it is what the reserved-quantity
-- count reads.
CREATE INDEX IF NOT EXISTS inventory_reservations_item_idx
    ON inventory_reservations (inventory_item_id, location_id)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS inventory_reservations_line_item_idx
    ON inventory_reservations (line_item_id)
    WHERE line_item_id IS NOT NULL;
