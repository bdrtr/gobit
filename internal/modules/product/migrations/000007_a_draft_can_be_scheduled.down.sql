DROP INDEX IF EXISTS product_publish_due_idx;
ALTER TABLE product
    DROP CONSTRAINT IF EXISTS product_publish_at_draft_only,
    DROP COLUMN IF EXISTS publish_at;
