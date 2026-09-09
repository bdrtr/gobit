-- The column goes first: dropping the table while a product still references it
-- would fail, and dropping the constraint alone would leave a column naming a
-- table that is gone.
--
-- What a rollback costs is stated rather than hidden: every product's type is
-- lost, and a tax rule written against a type stops matching -- it does not
-- match something ELSE, because the request's product_type_id goes back to
-- being empty and an empty reference produces no match key.
DROP INDEX IF EXISTS product_type_idx;
ALTER TABLE product DROP COLUMN IF EXISTS type_id;
DROP INDEX IF EXISTS product_type_handle_uniq;
DROP TABLE IF EXISTS product_type;
