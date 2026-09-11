-- Forgetting every registration that had not been proven yet.
--
-- No account is lost: a row here is a claim on an address and nothing more.
-- Everybody holding an unused link has to register again.
DROP INDEX IF EXISTS customer_registrations_expires_idx;
DROP TABLE IF EXISTS customer_registrations;
