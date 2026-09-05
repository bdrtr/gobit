-- Dropping the column would drop the constraint with it, because the constraint
-- names archived_at. It is dropped explicitly anyway: a rollback that depends
-- on a cascade is a rollback whose reader has to know the cascade rule.
ALTER TABLE orders
    DROP CONSTRAINT IF EXISTS orders_archived_stamp;

ALTER TABLE orders
    DROP COLUMN IF EXISTS archived_at;
