-- Rolling 000003 back: both columns and the partial index of 000001 return.
--
-- The rows come back with deleted_at NULL, the value every row had while the
-- columns existed — nothing ever wrote them, so nothing is lost by restoring
-- them empty.
--
-- The index is dropped and rebuilt rather than left alone: it exists here over
-- EVERY row, and a down that left it would leave the schema one step short of
-- where 000001 put it while looking finished. The next up would then skip it
-- because of IF NOT EXISTS and the difference would stand forever.
ALTER TABLE country ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
ALTER TABLE currency ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

DROP INDEX IF EXISTS country_region_id_idx;
CREATE INDEX IF NOT EXISTS country_region_id_idx ON country (region_id) WHERE deleted_at IS NULL;
