-- A customer group can be a segment whose members a rule decides (ADR 0217).
--
-- segment is the rule, as the service normalized it; a group without one is
-- managed by hand as before. segment_set_at names the rule: the job that writes
-- a segment's members checks it under the group's lock, so a pass does not write
-- the members of a rule that was replaced while it ran. segment_evaluated_at is
-- when a pass last finished writing them.
ALTER TABLE customer_group
    ADD COLUMN IF NOT EXISTS segment              JSONB,
    ADD COLUMN IF NOT EXISTS segment_set_at       TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS segment_evaluated_at TIMESTAMPTZ;

ALTER TABLE customer_group
    ADD CONSTRAINT customer_group_segment_named
        CHECK ((segment IS NULL) = (segment_set_at IS NULL)),
    ADD CONSTRAINT customer_group_segment_object
        CHECK (segment IS NULL OR jsonb_typeof(segment) = 'object'),
    ADD CONSTRAINT customer_group_segment_evaluated
        CHECK (segment IS NOT NULL OR segment_evaluated_at IS NULL);

-- The job reads the segments, a handful of groups, on every pass.
CREATE INDEX IF NOT EXISTS customer_group_segment_idx
    ON customer_group (id)
    WHERE segment IS NOT NULL AND deleted_at IS NULL;
