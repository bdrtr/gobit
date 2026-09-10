-- The counts go and the buy rules go with them.
--
-- The rules are DELETED rather than left behind: a CHECK constraint is evaluated
-- against every row of the table, deleted_at included, so restoring the two-value
-- constraint over a table still holding a "buy" row would fail the rollback
-- itself. They are also meaningless once the counts are gone — a buy condition
-- with nothing to reward is a rule no computation reads.
DELETE FROM promotion_rule WHERE rule_type = 'buy';

ALTER TABLE promotion_rule
    DROP CONSTRAINT IF EXISTS promotion_rule_type_check;
ALTER TABLE promotion_rule
    ADD CONSTRAINT promotion_rule_type_check CHECK (rule_type IN ('context', 'target'));

ALTER TABLE promotion_application_method
    DROP CONSTRAINT IF EXISTS promotion_application_method_reward_pair_check,
    DROP CONSTRAINT IF EXISTS promotion_application_method_buy_qty_check,
    DROP CONSTRAINT IF EXISTS promotion_application_method_apply_qty_check;

ALTER TABLE promotion_application_method
    DROP COLUMN IF EXISTS buy_quantity,
    DROP COLUMN IF EXISTS apply_to_quantity;
