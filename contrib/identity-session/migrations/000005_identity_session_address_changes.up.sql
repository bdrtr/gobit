-- An address change that is waiting for its link to be followed (ADR 0377).
--
-- A row exists only for a customer who HAS a credential here, for
-- customer_password_resets' reason, and its foreign key cascades for the same
-- one: erasing the credential erases the pending change.
CREATE TABLE IF NOT EXISTS customer_address_changes (
    -- The SHA-256 of the token that was sent, hex: a leaked table must not be
    -- a list of links.
    token_hash  TEXT        PRIMARY KEY,
    -- The customer the link moves. UNIQUE, so asking again REPLACES the
    -- pending change and only the newest link works.
    customer_id TEXT        NOT NULL UNIQUE
        REFERENCES customer_credentials (customer_id) ON DELETE CASCADE,
    -- The address the account moves to, folded as customer_credentials keeps
    -- its own.
    email       TEXT        NOT NULL,
    -- When the link stops working.
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT customer_address_changes_email_folded
        CHECK (email = lower(btrim(email)) AND email <> ''),
    CONSTRAINT customer_address_changes_expires_after_creation
        CHECK (expires_at > created_at)
);

-- An erasure finds a pending change by the address it would move to.
CREATE INDEX IF NOT EXISTS customer_address_changes_email_idx
    ON customer_address_changes (email);
