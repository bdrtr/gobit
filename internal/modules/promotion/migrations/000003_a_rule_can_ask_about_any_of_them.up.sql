-- A rule can ask "is this customer in ANY of these groups".
--
-- # What could not be asked
--
-- A customer belongs to as many groups as the merchant put them in, and the cart
-- could send only ONE of them: the merchant-ranked head (ADR 0049). So a customer
-- in {retail, vip} whose head is retail did not match a rule reading
-- `customer_group_id in [vip]` — a segment discount silently not applying to
-- somebody who IS in the segment, which is the defect ADR 0103 opens with.
--
-- The engine's attribute namespace was never the limit; the WIRE was. One string
-- per attribute, and an operator that compares one value to a list. What was
-- missing is an operator that compares a LIST to a list (ADR 0144).
--
-- # Why a ninth operator and not a change to `in`
--
-- Because `in` is shipped. A rule written as `customer_group_id in [vip]` means
-- "the group we picked for this cart is vip" to every installation running today,
-- and teaching it to read the whole list would start discounting customers whose
-- head is not vip — a live discount changing with nothing announcing it. The new
-- operator is the only one that reads the list side.
ALTER TABLE promotion_rule
    DROP CONSTRAINT IF EXISTS promotion_rule_operator_check;
ALTER TABLE promotion_rule
    ADD CONSTRAINT promotion_rule_operator_check
    CHECK (operator IN ('eq', 'ne', 'in', 'nin', 'any_in', 'gt', 'gte', 'lt', 'lte'));
