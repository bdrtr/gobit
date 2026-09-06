-- Rolling 000002 back: the column and the two partial indexes of 000001 return.
--
-- The rows come back with deleted_at NULL, which is the value every row had
-- while the column existed — nothing ever wrote it, so no history is lost by
-- restoring it empty.
--
-- The indexes are dropped and rebuilt rather than left alone: they exist here
-- with a NARROWER predicate than 000001's, and a down that left them in place
-- would leave the schema one step short of where 000001 put it. The next up
-- would then find them already present, skip them because of IF NOT EXISTS and
-- leave the difference standing forever.
ALTER TABLE inventory_reservations ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

DROP INDEX IF EXISTS inventory_reservations_item_idx;
DROP INDEX IF EXISTS inventory_reservations_line_item_idx;

CREATE INDEX IF NOT EXISTS inventory_reservations_item_idx
    ON inventory_reservations (inventory_item_id, location_id)
    WHERE status = 'active' AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS inventory_reservations_line_item_idx
    ON inventory_reservations (line_item_id)
    WHERE line_item_id IS NOT NULL AND deleted_at IS NULL;
