-- Rollback of the promotion schema (plan Section 8: every up has a down twin).
--
-- The order is the REVERSE of the dependency: first the tables that reference
-- promotion/campaign, then promotion, and campaign last. CASCADE is not used —
-- seeing a link that was overlooked as an ERROR is better than having it
-- silently dropped.
--
-- The indexes fall with their tables; there is no need to DROP them separately.
DROP TABLE IF EXISTS promotion_redemption;
DROP TABLE IF EXISTS promotion_rule;
DROP TABLE IF EXISTS promotion_application_method;
DROP TABLE IF EXISTS promotion;
DROP TABLE IF EXISTS campaign;
