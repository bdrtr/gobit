-- Rolling the region schema back. The order is the reverse of the foreign key
-- dependencies: the dependent tables go first.
DROP INDEX IF EXISTS country_region_id_idx;
DROP TABLE IF EXISTS country;

DROP INDEX IF EXISTS region_currency_code_idx;
DROP TABLE IF EXISTS region;

DROP TABLE IF EXISTS currency;
