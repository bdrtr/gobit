-- Forgetting which relying party a credential belongs to.
--
-- The rows survive; what is lost is the module's ability to tell a usable key
-- from one left behind by a domain move.
CREATE INDEX IF NOT EXISTS passkey_credentials_customer_idx
    ON passkey_credentials (customer_id);
DROP INDEX IF EXISTS passkey_credentials_customer_rp_idx;

ALTER TABLE passkey_credentials
    DROP CONSTRAINT IF EXISTS passkey_credentials_rp_id_not_blank;
ALTER TABLE passkey_credentials
    DROP COLUMN IF EXISTS rp_id;
