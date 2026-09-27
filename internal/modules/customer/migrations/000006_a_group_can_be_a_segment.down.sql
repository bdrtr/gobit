DROP INDEX IF EXISTS customer_group_segment_idx;

ALTER TABLE customer_group
    DROP CONSTRAINT IF EXISTS customer_group_segment_evaluated,
    DROP CONSTRAINT IF EXISTS customer_group_segment_object,
    DROP CONSTRAINT IF EXISTS customer_group_segment_named,
    DROP COLUMN IF EXISTS segment_evaluated_at,
    DROP COLUMN IF EXISTS segment_set_at,
    DROP COLUMN IF EXISTS segment;
