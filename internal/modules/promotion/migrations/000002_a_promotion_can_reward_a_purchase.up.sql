-- The application method learns how many units are bought and how many are
-- rewarded, and a rule learns to name the purchase.
--
-- # Why two counts and not one
--
-- "Buy two, get one" is two numbers about two different sets: the units that
-- must be in the cart, and the units the discount lands on. One number cannot
-- say it, and reusing max_quantity would have said something else — that field
-- bounds how many units of a SINGLE line a fixed discount repeats over, while
-- this one bounds the whole reward across every line the target rules selected.
--
-- # Why the pair is a CHECK and the promotion type is not
--
-- A method belongs to a promotion whose type lives in another table, so no CHECK
-- here can say "buyget rows carry the counts". What this table CAN hold is the
-- pairing, and it holds it: either both counts are written or neither is. A
-- method with a buy quantity and no reward quantity rewards nothing; the reverse
-- gives away a discount nobody earned. The half that needs both tables is
-- decided where both are read — the computation refuses to apply a buyget whose
-- method carries neither, and says so with a reason (ADR 0110).
ALTER TABLE promotion_application_method
    ADD COLUMN buy_quantity      BIGINT,
    ADD COLUMN apply_to_quantity BIGINT;

ALTER TABLE promotion_application_method
    ADD CONSTRAINT promotion_application_method_reward_pair_check CHECK (
        (buy_quantity IS NULL) = (apply_to_quantity IS NULL)
    ),
    ADD CONSTRAINT promotion_application_method_buy_qty_check CHECK (
        buy_quantity IS NULL OR (buy_quantity >= 1 AND buy_quantity <= 1000000)
    ),
    ADD CONSTRAINT promotion_application_method_apply_qty_check CHECK (
        apply_to_quantity IS NULL OR (apply_to_quantity >= 1 AND apply_to_quantity <= 1000000)
    );

-- rule_type gains "buy": the lines that COUNT toward the purchase.
--
-- It is a third value beside "context" and "target" rather than a flag on
-- "target", because what is bought and what is rewarded are two different sets
-- of lines — "buy two shirts, get a tie" cannot be written with one of them.
ALTER TABLE promotion_rule
    DROP CONSTRAINT IF EXISTS promotion_rule_type_check;
ALTER TABLE promotion_rule
    ADD CONSTRAINT promotion_rule_type_check CHECK (rule_type IN ('context', 'target', 'buy'));
