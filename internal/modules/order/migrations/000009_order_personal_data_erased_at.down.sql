-- Dropping the column would drop the constraint with it, because the constraint
-- names personal_data_erased_at. It is dropped explicitly anyway, for the
-- reason 000007's rollback gives: a rollback that depends on a cascade is a
-- rollback whose reader has to know the cascade rule.
--
-- What this rollback CANNOT restore is the e-mail and the address that were
-- nulled while the column existed, and that is the point of the migration
-- rather than a defect of it: those rows were rewritten because a person asked
-- to be forgotten, and a rollback that brought their contact details back would
-- be the schema undoing an erasure. What is given up here is only the RECORD OF
-- WHEN it happened — after this runs, an erased order is once again
-- indistinguishable from an order that never carried an e-mail.
ALTER TABLE orders
    DROP CONSTRAINT IF EXISTS orders_personal_data_erased_stamp;

ALTER TABLE orders
    DROP COLUMN IF EXISTS personal_data_erased_at;
