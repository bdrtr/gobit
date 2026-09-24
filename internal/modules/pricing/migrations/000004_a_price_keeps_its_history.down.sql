-- Rolling back forgets the history, which is what the module did before ADR
-- 0167: the prices themselves are untouched, and the service answers only for
-- the present again.
DROP TABLE IF EXISTS price_list_history;
DROP TABLE IF EXISTS price_set_history;
