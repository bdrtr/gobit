DROP INDEX IF EXISTS product_archive_due_idx;
ALTER TABLE product
    DROP CONSTRAINT IF EXISTS product_archive_after_publish,
    DROP CONSTRAINT IF EXISTS product_archive_at_live_only,
    DROP COLUMN IF EXISTS archive_at;
