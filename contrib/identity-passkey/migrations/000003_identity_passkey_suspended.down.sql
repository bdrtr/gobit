-- Forgetting which keys were suspended: every one of them signs in again.
ALTER TABLE passkey_credentials DROP COLUMN IF EXISTS suspended_at;
