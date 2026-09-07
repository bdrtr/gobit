DROP INDEX IF EXISTS product_option_value_folded_uniq;

ALTER TABLE product_option_value
    DROP COLUMN IF EXISTS value_folded;
