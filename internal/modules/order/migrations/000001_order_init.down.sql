-- Rollback of 000001_order_init.
--
-- The tables are dropped in the REVERSE of dependency order: first the tables
-- that reference orders, then orders. Indexes drop together with the table and
-- are not DROPped separately. display_id's IDENTITY sequence belongs to the
-- column as well, so it falls together with orders; a separate DROP SEQUENCE is
-- not needed.

DROP TABLE IF EXISTS order_claims;
DROP TABLE IF EXISTS order_exchanges;
DROP TABLE IF EXISTS order_returns;
DROP TABLE IF EXISTS order_summaries;
DROP TABLE IF EXISTS order_line_items;
DROP TABLE IF EXISTS orders;
