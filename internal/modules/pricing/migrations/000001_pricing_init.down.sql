-- Rolling the pricing schema back. The order is the reverse of the foreign key
-- dependencies: the dependent tables drop first.
DROP INDEX IF EXISTS price_rule_price_id_idx;
DROP TABLE IF EXISTS price_rule;

DROP INDEX IF EXISTS price_list_id_idx;
DROP INDEX IF EXISTS price_set_id_idx;
DROP TABLE IF EXISTS price;

DROP TABLE IF EXISTS price_set;
DROP TABLE IF EXISTS price_list;
