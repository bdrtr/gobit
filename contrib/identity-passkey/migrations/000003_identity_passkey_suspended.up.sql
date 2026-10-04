-- A key whose signature counter did not advance, which is what a copied key
-- does (ADR 0382). NULL is a key that signs in; a suspended one signs nobody in
-- and is not a way into the account until it is removed.
ALTER TABLE passkey_credentials ADD COLUMN IF NOT EXISTS suspended_at TIMESTAMPTZ;
