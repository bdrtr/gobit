-- Rolling 000008 back drops the mark; the held count that reads it goes with
-- the code that ADR 0423's upgrade shipped.
--
-- It is not undone by applying 000008 again. The up's backfill marks every
-- parcel then 'returned' as held whole, so a parcel that came back under ADR
-- 0423's rule becomes held whole on the way back up, and its units stop being
-- owed again. Do not roll 000008 back and forth on a live database.
ALTER TABLE fulfillments
    DROP COLUMN IF EXISTS held_whole;
