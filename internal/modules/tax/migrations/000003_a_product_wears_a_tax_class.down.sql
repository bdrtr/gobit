-- The rollback removes what this version made possible, and says which rows
-- those are.
--
-- A rule written against a class cannot be expressed by the older vocabulary,
-- so it is DELETED rather than left to break the narrowed CHECK. That is not
-- inventing history: a rate rule is CONFIGURATION — the merchant's statement
-- about which rate applies to what — and a rollback is the operator asking for
-- the older shape back. The rows deleted here are exactly the ones the older
-- shape has no word for, and no fact about anything that happened is lost with
-- them. (An order's tax is not read from here: the line stores the rate it was
-- charged, see order migration 000004.)
--
-- The soft-deleted ones go too. A retired rule still occupies its row and the
-- CHECK reads every row, deleted or not; leaving them would make the rollback
-- fail for a shop that once wrote a class rule and then thought better of it.
DELETE FROM tax_rate_rule WHERE reference = 'tax_class';

ALTER TABLE tax_rate_rule DROP CONSTRAINT IF EXISTS tax_rate_rule_reference_check;

ALTER TABLE tax_rate_rule
    ADD CONSTRAINT tax_rate_rule_reference_check
        CHECK (reference IN ('product', 'product_type', 'shipping_option'));

DROP INDEX IF EXISTS tax_class_member_class_idx;
DROP INDEX IF EXISTS tax_class_member_product_uniq;
DROP TABLE IF EXISTS tax_class_member;

DROP INDEX IF EXISTS tax_class_name_uniq;
DROP TABLE IF EXISTS tax_class;
