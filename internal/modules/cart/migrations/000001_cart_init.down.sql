-- Rollback of 000001_cart_init.
--
-- Tables are dropped in the REVERSE of dependency order: first the tables that
-- reference carts, then carts. Indexes fall together with their table, they are
-- not DROPped separately.

DROP TABLE IF EXISTS cart_shipping_methods;
DROP TABLE IF EXISTS cart_addresses;
DROP TABLE IF EXISTS cart_line_items;
DROP TABLE IF EXISTS carts;
