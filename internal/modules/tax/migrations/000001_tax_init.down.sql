-- Reverting the tax schema. The order is the reverse of the foreign key
-- dependencies: the rules drop first, then the rates, and the regions last. A
-- DROP in the opposite order would blow up with "there are still dependent
-- objects" and would leave golang-migrate's version ledger dirty — from that
-- point on the module CANNOT be migrated again (see internal/arch
-- TestMigrationsCanReallyBeRolledBack).
--
-- Indexes drop together with their tables; they are still written out
-- explicitly, so that the reverse path stays complete when an index is later
-- added by a separate migration.
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
