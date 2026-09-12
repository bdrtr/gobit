-- Rolling back drops the waiting enrolment.
--
-- That is the right loss: a pending secret is one nobody has proven, so the
-- person is still signing in with the confirmed one, and the column's absence
-- takes the enrolment back to "start it again".
ALTER TABLE auth_mfa_credential
    DROP CONSTRAINT IF EXISTS auth_mfa_credential_pending_needs_confirmed;

ALTER TABLE auth_mfa_credential
    DROP CONSTRAINT IF EXISTS auth_mfa_credential_pending_not_empty;

ALTER TABLE auth_mfa_credential
    DROP COLUMN IF EXISTS pending_secret;
