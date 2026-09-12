-- A confirmed second factor is DEMANDED at login, so it can no longer be
-- replaced by an enrolment nobody proved.
--
-- # What the old shape did, and why enforcement breaks it
--
-- One row per user, and re-enrolling replaced it: the secret was overwritten and
-- `confirmed_at` went back to NULL. Nothing enforced the factor, so that was
-- harmless — and it was also what this table's own comment promised a person with
-- a lost phone, "ask again, scan the new code".
--
-- Both stop being true the moment a login demands the factor. A person whose
-- phone is gone cannot reach the enrolment endpoint at all (they cannot sign in),
-- and a person who starts an enrolment and walks away would have had their
-- confirmation cleared — which would silently turn the demand OFF for their next
-- login. An abandoned scan must not be a way out of the second factor.
--
-- # So a new secret WAITS beside the proven one
--
-- `pending_secret` holds the enrolment that has not been proven yet. The
-- confirmed secret keeps working — the old phone keeps signing in — and the
-- confirmation moves to the new secret only when a code from it arrives, in one
-- statement. There is no window in which the account has no factor and no window
-- in which two secrets are accepted.
ALTER TABLE auth_mfa_credential
    ADD COLUMN IF NOT EXISTS pending_secret BYTEA;

-- An empty ciphertext is not a secret waiting; it is a write that went wrong.
ALTER TABLE auth_mfa_credential
    ADD CONSTRAINT auth_mfa_credential_pending_not_empty
    CHECK (pending_secret IS NULL OR length(pending_secret) > 0);

-- A pending secret only means something beside a CONFIRMED one.
--
-- Before the first confirmation there is nothing to keep alive, so an enrolment
-- overwrites `secret` and leaves this NULL. The constraint is what makes that a
-- rule rather than a habit of the code above it: a row with a pending secret and
-- no confirmation would leave "which of these two does the login accept"
-- answerable two ways.
ALTER TABLE auth_mfa_credential
    ADD CONSTRAINT auth_mfa_credential_pending_needs_confirmed
    CHECK (pending_secret IS NULL OR confirmed_at IS NOT NULL);
