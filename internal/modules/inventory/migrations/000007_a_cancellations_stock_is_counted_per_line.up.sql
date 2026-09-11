-- How many of a line's written-off units are back on the shelf becomes a
-- QUESTION the ledger can answer, instead of a fact spread across two acts.
--
-- # What was wrong
--
-- ADR 0134 put units back with `min(canceled, bought - in a live parcel)` and
-- ADR 0139 released what a canceled parcel had been holding, as the DIFFERENCE
-- between that window evaluated twice. Each act was correct on its own and the
-- pair assumed an ORDER nothing enforces: the parcel act's "what was already
-- owed" term assumes the line cancellations have run. The bus is asynchronous and
-- at-least-once, so a write-off whose direct publish was lost and whose outbox
-- relay is a minute behind arrives AFTER an operator cancels the parcel.
--
-- Measured, five units bought and all five written off with three in a parcel:
-- the parcel act put back 3 anticipating the line act, the line act then found a
-- full window and put back 5, and the shelf was credited with EIGHT units for a
-- cancellation of five. The two acts carry different references, so the ledger's
-- uniqueness could not see it (gap D82).
--
-- # What replaces it
--
-- Both acts now compute the same TARGET — `min(canceled, bought - committed)` —
-- and the module brings the line's total UP TO it under the level's lock. The
-- order of the two acts stops mattering, because neither adds a delta it computed
-- from a state it did not hold.
--
-- That requires knowing how much of a line is already back, which is a sum over
-- this table, which needs the line.
ALTER TABLE inventory_movements
    ADD COLUMN IF NOT EXISTS line_item_id TEXT;

-- It belongs to the order module and is NOT a foreign key (Principle 2.2, the
-- same rule reference already follows). It is set on a cancellation and on
-- nothing else: on a sale the units leave against a reservation, and attributing
-- them to a line as well would be a second answer to a question the reservation
-- already answers.
ALTER TABLE inventory_movements
    ADD CONSTRAINT inventory_movements_line_only_on_cancellation
    CHECK (line_item_id IS NULL OR reason = 'cancellation');

ALTER TABLE inventory_movements
    ADD CONSTRAINT inventory_movements_line_not_blank
    CHECK (line_item_id IS NULL OR length(btrim(line_item_id)) > 0);

-- The sum this makes possible, per line AND item.
--
-- The item is in the key because it is in the predicate, and it is in the
-- predicate because the LOCK is per (item, location): a sum reaching across items
-- would read rows the caller's transaction does not hold.
CREATE INDEX IF NOT EXISTS inventory_movements_cancellation_line_idx
    ON inventory_movements (line_item_id, inventory_item_id)
    WHERE reason = 'cancellation' AND line_item_id IS NOT NULL;

-- The uniqueness on the reference is DROPPED, and it is not a loosening.
--
-- It held one write per cancellation, which was the idempotency of the old
-- design: an act added a delta once. Under "bring the total up to the target"
-- the guard is the sum read under the lock, and the same act can legitimately
-- write TWICE — a write-off that could only reach two units while a parcel was
-- live tops up by three when that parcel is canceled, and it carries the same
-- reference both times. Keeping the index would refuse the correct second write.
--
-- What replaces it as the protection against a redelivered event is stronger
-- rather than weaker: a second delivery computes the same target, reads the same
-- sum, and writes nothing at all.
DROP INDEX IF EXISTS inventory_movements_cancellation_once_idx;
