-- An administrator can enroll an authenticator app.
--
-- # What this table holds that no other one in this module does
--
-- A secret it can READ BACK. A password is argon2id, an API key and an invitation
-- token are SHA-256, and every one of them is stored so that the database cannot
-- produce the original. Verifying a six-digit TOTP code means recomputing it,
-- which means holding what the authenticator app holds.
--
-- So the column is a CIPHERTEXT, sealed with a key the installation supplies
-- (MFA_SECRET_KEY) and never with a default. Without a key the module refuses to
-- enroll rather than storing a plaintext secret, which is the answer ADR 0007
-- gives for an unconfigured authenticator: a security feature that quietly does
-- less than its name is worse than one that is visibly absent.
--
-- What the encryption buys is precise: it defends a database read WITHOUT the
-- process — a stolen backup, a replica, an injection that can select. It does not
-- defend a compromised host, because a process that can decrypt is one an
-- attacker who owns it can use.
CREATE TABLE IF NOT EXISTS auth_mfa_credential (
    -- ONE credential per user, so the primary key is the user.
    --
    -- Re-enrolling REPLACES rather than adding, and that is the behaviour a person
    -- who lost their phone needs: they ask again, scan the new code, and the old
    -- secret stops working the moment the new one is confirmed. A second row would
    -- leave the lost phone able to sign in.
    user_id      TEXT        PRIMARY KEY REFERENCES auth_user (id) ON DELETE CASCADE,

    -- secret is nonce || AES-GCM ciphertext. It is BYTEA and not TEXT because it
    -- is bytes; base64 in a text column would be one encoding nobody asked for.
    secret       BYTEA       NOT NULL,

    -- confirmed_at separates "enrolled" from "proven".
    --
    -- An enrollment writes the secret and leaves this NULL: at that moment nobody
    -- has shown that the app holds the same secret, and a credential that counted
    -- before the first correct code would lock out anybody whose scan failed
    -- halfway. It is stamped by the confirm endpoint and by nothing else.
    confirmed_at TIMESTAMPTZ,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT auth_mfa_credential_secret_not_empty CHECK (length(secret) > 0)
);

-- Finding the credentials that were never proven, which is the one question an
-- operator asks of this table without naming a user: an enrollment that stayed
-- unconfirmed is somebody who tried and gave up.
CREATE INDEX IF NOT EXISTS auth_mfa_credential_unconfirmed_idx
    ON auth_mfa_credential (created_at)
    WHERE confirmed_at IS NULL;
