-- Restores the constraint to its 000001 form, the one that lets an empty array
-- through, exactly as it was written.
ALTER TABLE promotion_rule DROP CONSTRAINT IF EXISTS promotion_rule_values_check;

ALTER TABLE promotion_rule
    ADD CONSTRAINT promotion_rule_values_check CHECK (array_length(rule_values, 1) >= 1);
