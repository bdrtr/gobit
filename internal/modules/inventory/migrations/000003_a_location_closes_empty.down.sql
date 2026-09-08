-- Rolling 000003 back: deleted_at and the index of 000001 return.
--
-- The closing stamps are LOST, and that is the honest reading of the rollback
-- rather than a shortcut. deleted_at cannot hold them: a closed location is
-- answered for by every read and a soft-deleted one is hidden from all of them,
-- so moving the stamps across would hide rows that levels and reservations
-- still name. What comes back is the schema of 000002, where a location has no
-- retirement at all.
--
-- Nothing starts selling again because of it. A location could only be closed
-- while EMPTY (ADR 0055), so the ones this statement puts back into service have
-- no stock to offer; what the shop loses is the record that they were retired.
--
-- The index is dropped and rebuilt for the reason 000002's down gives: the
-- DROP COLUMN below takes stock_locations_open_idx with it, silently, because
-- the predicate names the column. The CREATE that follows is 000001's index,
-- under 000001's name.
ALTER TABLE stock_locations DROP COLUMN IF EXISTS closed_at;
ALTER TABLE stock_locations ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS stock_locations_alive_idx
    ON stock_locations (created_at DESC)
    WHERE deleted_at IS NULL;
