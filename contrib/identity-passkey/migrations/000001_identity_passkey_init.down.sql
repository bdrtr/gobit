-- Dropping this table forgets every passkey.
--
-- It takes nothing else: a customer who also has a password keeps it, and their
-- orders and addresses live in gobit's own tables. What is lost is every
-- registered key, and every person holding one has to register again.
DROP INDEX IF EXISTS passkey_credentials_customer_idx;
DROP TABLE IF EXISTS passkey_credentials;
