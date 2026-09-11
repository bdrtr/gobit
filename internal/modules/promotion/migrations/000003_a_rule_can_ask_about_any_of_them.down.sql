-- The reverse of 000003.
--
-- It fails LOUDLY if a rule is using the operator, and that is the correct
-- outcome: narrowing the CHECK while a row violates it is not something a
-- migration may do quietly. The operator's way out is to delete or rewrite those
-- rules first, and a discount changing is a thing they should decide rather than
-- discover.
ALTER TABLE promotion_rule
    DROP CONSTRAINT IF EXISTS promotion_rule_operator_check;
ALTER TABLE promotion_rule
    ADD CONSTRAINT promotion_rule_operator_check
    CHECK (operator IN ('eq', 'ne', 'in', 'nin', 'gt', 'gte', 'lt', 'lte'));
