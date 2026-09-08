-- stock_locations trades deleted_at for closed_at.
--
-- # Writing deleted_at would not have been enough
--
-- Nothing ever wrote the column, and the audit that found it (docs/gaps.md
-- D18) also measured why writing it was not the fix. Availability is summed
-- off inventory_levels with NO join to this table — AvailableQuantityByItemIDs
-- and ListInventoryLevels — so a location hidden behind "deleted_at IS NULL"
-- would keep selling its stock while disappearing from the operator's screen,
-- and a reservation would still be handed out at it. A hard delete is worse:
-- inventory_levels.location_id and inventory_reservations.location_id both
-- CASCADE, so it would destroy the stock rows and the reservation history that
-- 000002 says must never be deleted.
--
-- # What this column means instead
--
-- ADR 0055: a location CLOSES EMPTY. The service refuses to close one that
-- still holds stock or a live promise, and refuses to write stock into a closed
-- one, so the sums above stay right WITHOUT a join: a closed location has
-- nothing left to add to them. The row itself stays READABLE, because every
-- level and every reservation names it and a warehouse no read can resolve
-- makes that history unreadable.
--
-- That is precisely what deleted_at could not carry. In this repository the
-- column means "hidden from every read"; this row is retired and still
-- answered for. Two different facts, so two different columns, rather than one
-- column renamed into a second meaning. Nothing is lost by dropping it: it was
-- NULL on every row that has ever existed.
--
-- # The index goes with the column, and that was measured before 000002
--
-- PostgreSQL drops any index whose PREDICATE names a dropped column, silently
-- and without a notice; 000002 measured it on a real PostgreSQL 16 before it
-- dropped this module's other deleted_at. stock_locations_alive_idx is partial
-- on deleted_at, so the DROP below takes it. The index that follows is the same
-- index under the new predicate, and it is RENAMED with it: "alive" is no
-- longer what the predicate says.
ALTER TABLE stock_locations DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE stock_locations ADD COLUMN IF NOT EXISTS closed_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS stock_locations_open_idx
    ON stock_locations (created_at DESC)
    WHERE closed_at IS NULL;
