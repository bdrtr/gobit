-- Dropping the column returns every market to tax-exclusive quoting, which is
-- what the schema meant before it existed. No data is derived from it, so
-- nothing else has to be undone.
ALTER TABLE tax_region
    DROP COLUMN IF EXISTS prices_include_tax;
