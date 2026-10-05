-- A rate that stands on another is never the region's default (gap D248).
--
-- Migration 000004 left the rule to the service, which refused it at creation
-- and not on an update, so a standing rate could carry the flag. The
-- calculation drops a standing rate from the candidates, so such a row was
-- never the default it was listed as: the region's lines matching no rule were
-- taxed at zero, and the partial unique index kept the real default out.
--
-- The flag is cleared on every such row, retired ones included, as the CHECK
-- reads every row. Nothing a line is taxed at changes with it: the region
-- had no default in the calculation before and has none after, and the
-- operator can now make the base the default.
UPDATE tax_rate
SET is_default = FALSE,
    updated_at = now()
WHERE is_default AND stacks_on_id IS NOT NULL;

-- NULL-SAFE BY CONSTRUCTION: is_default is NOT NULL, so the right side cannot
-- be NULL and a standing row passes only with the flag down.
ALTER TABLE tax_rate
    ADD CONSTRAINT tax_rate_standing_default_check
        CHECK (stacks_on_id IS NULL OR NOT is_default);
