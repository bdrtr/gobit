-- The column goes and takes what merchants wrote in it with it. That is the
-- ordinary price of a rollback over data, and it is stated rather than hidden:
-- nothing else in this module reads the field, so a rolled-back schema prices
-- and selects exactly as it did before.
ALTER TABLE price_list
    DROP COLUMN IF EXISTS metadata;
