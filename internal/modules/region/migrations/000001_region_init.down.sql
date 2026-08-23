-- region şemasının geri alınması. Sıra, foreign key bağımlılıklarının
-- tersidir: önce bağımlı tablolar düşer.
DROP INDEX IF EXISTS country_region_id_idx;
DROP TABLE IF EXISTS country;

DROP INDEX IF EXISTS region_currency_code_idx;
DROP TABLE IF EXISTS region;

DROP TABLE IF EXISTS currency;
