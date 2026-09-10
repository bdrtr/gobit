-- Dropping this table logs every customer out and forgets every password.
--
-- It takes no data a shop needs to trade: the customers, their orders and their
-- addresses live in gobit's own tables and are untouched. What is lost is the
-- ability to SIGN IN, and an installation rolling this back should expect the
-- four storefront routes ADR 0125 closed to close again.
DROP TABLE IF EXISTS customer_credentials;
