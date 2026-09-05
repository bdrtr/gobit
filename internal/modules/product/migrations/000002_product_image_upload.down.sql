-- Dropping the column drops the FORWARD half of the binding for good: which
-- upload an image was made from is written nowhere else in this module.
--
-- The REVERSE half survives, and that asymmetry is deliberate rather than
-- tidy. The "upload_product_image" link table belongs to the core's link schema
-- and ADR 0005 gives that schema no down path at all: a definition that
-- disappears leaves its table in place, because dropping it automatically would
-- let a deployment mistake erase every binding in the installation. So a rolled
-- back product module leaves link rows pointing at image ids whose upload
-- column is gone; re-applying the up migration does NOT refill the column from
-- them (no data migration is attempted here — the rows may have been written by
-- a newer version whose meaning this file cannot know).
--
-- The CHECK constraint is defined ON the column and goes with it.
ALTER TABLE product_image
    DROP COLUMN IF EXISTS upload_id;
