DROP TABLE IF EXISTS product_revision;
ALTER TABLE product DROP CONSTRAINT IF EXISTS product_version_not_negative;
ALTER TABLE product DROP COLUMN IF EXISTS version;
