DROP INDEX IF EXISTS carts_opened_by_idx;
ALTER TABLE carts DROP CONSTRAINT IF EXISTS carts_opened_by_not_blank;
ALTER TABLE carts DROP COLUMN IF EXISTS opened_by;
