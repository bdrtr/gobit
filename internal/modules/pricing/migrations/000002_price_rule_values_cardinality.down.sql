-- Restores the constraint to its 000001 form — the one that lets an empty array
-- through.
--
-- The rollback writes the old definition EXACTLY as it was; leaving a "better"
-- version behind would break down's promise to return the schema to its
-- previous version.
ALTER TABLE price_rule DROP CONSTRAINT IF EXISTS price_rule_values_check;

ALTER TABLE price_rule
    ADD CONSTRAINT price_rule_values_check CHECK (array_length(rule_values, 1) >= 1);
