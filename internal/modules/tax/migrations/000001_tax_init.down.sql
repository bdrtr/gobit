-- tax şemasının geri alınması. Sıra, foreign key bağımlılıklarının tersidir:
-- önce kurallar, sonra oranlar, en sonda bölgeler düşer. Ters sırada bir DROP
-- "hâlâ bağımlı nesneler var" hatasıyla patlar ve golang-migrate'in sürüm
-- defterini dirty bırakırdı — o noktadan sonra modül bir daha migrate EDİLEMEZ
-- (bkz. internal/arch TestMigrationlarGercektenGeriAlinabilir).
--
-- İndeksler tablolarıyla birlikte düşer; yine de açıkça yazılırlar ki bir
-- indeks ileride ayrı bir migration'la eklendiğinde geri alma yolu tam kalsın.
DROP INDEX IF EXISTS tax_rate_rule_rate_idx;
DROP INDEX IF EXISTS tax_rate_rule_uniq;
DROP TABLE IF EXISTS tax_rate_rule;

DROP INDEX IF EXISTS tax_rate_region_idx;
DROP INDEX IF EXISTS tax_rate_code_uniq;
DROP INDEX IF EXISTS tax_rate_default_uniq;
DROP TABLE IF EXISTS tax_rate;

DROP INDEX IF EXISTS tax_region_province_uniq;
DROP INDEX IF EXISTS tax_region_country_root_uniq;
DROP TABLE IF EXISTS tax_region;
