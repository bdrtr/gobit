-- A promotion rule holds at least one value, at the data level too (ADR 0169).
--
-- The CHECK (array_length(rule_values, 1) >= 1) in 000001 let an empty array
-- through: array_length('{}', 1) is NULL, and a CHECK whose result is NULL
-- passes. It blocked only a NULL column, which NOT NULL already blocks. The
-- pricing module found the same constraint on price_rule and replaced it in its
-- own 000002; this is the same repair on the sibling it was not carried to.
--
-- The service refuses a rule with no values, and matchRule reads one as
-- "does not match" rather than opening the promotion to everybody. What an
-- empty rule can still come from is a writer that is not the service: a
-- maintenance script or a partial restore.
--
-- Not NOT VALID, for pricing's reason: if the table already holds a rule with no
-- values, this migration fails on purpose, and says which constraint.
ALTER TABLE promotion_rule DROP CONSTRAINT IF EXISTS promotion_rule_values_check;

ALTER TABLE promotion_rule
    ADD CONSTRAINT promotion_rule_values_check CHECK (cardinality(rule_values) >= 1);
