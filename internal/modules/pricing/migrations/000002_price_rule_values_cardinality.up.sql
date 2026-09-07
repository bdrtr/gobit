-- Brings the price_rule.rule_values constraint to the form that ACTUALLY closes
-- the gate.
--
-- The CHECK (array_length(rule_values, 1) >= 1) in 000001 let an empty array
-- through: in PostgreSQL array_length('{}', 1) returns NULL, and a CHECK whose
-- result is NULL counts as SATISFIED. The constraint therefore blocked only a
-- NULL column — which NOT NULL already blocks — so in practice it blocked
-- nothing at all.
--
-- cardinality returns 0 for an empty array, not NULL; that is why this
-- constraint works. A rule with no values makes its condition unreadable to the
-- calculation (see matchRule in the service layer), and a maintenance script
-- running SQL directly, or a partial restore, can produce such a row; that is
-- why the gate has to stand at the data level too.
--
-- The constraint is deliberately NOT declared NOT VALID: if the table already
-- holds a rule with no values, this migration FAILS on purpose. A constraint
-- silently applied by halves is worse than believing in a gate that is not
-- there.
ALTER TABLE price_rule DROP CONSTRAINT IF EXISTS price_rule_values_check;

ALTER TABLE price_rule
    ADD CONSTRAINT price_rule_values_check CHECK (cardinality(rule_values) >= 1);
