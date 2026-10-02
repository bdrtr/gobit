-- A password reset that is waiting for its link to be followed (ADR 0373).
--
-- A row exists only for a customer who HAS a credential here: a reset replaces a
-- password, and a customer without one has nothing to replace. The foreign key
-- says so, and it is allowed because both tables are this module's. ON DELETE
-- CASCADE is what an erasure of the credential relies on: the pending reset goes
-- with it, and no second statement can forget it.
CREATE TABLE IF NOT EXISTS customer_password_resets (
    -- The SHA-256 of the token that was sent, hex, for the reason
    -- customer_registrations gives: a leaked table must not be a list of links.
    token_hash  TEXT        PRIMARY KEY,
    -- The customer whose password the link replaces. UNIQUE, so asking again
    -- REPLACES the pending reset and only the newest link works.
    customer_id TEXT        NOT NULL UNIQUE
        REFERENCES customer_credentials (customer_id) ON DELETE CASCADE,
    -- When the link stops working.
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT customer_password_resets_expires_after_creation
        CHECK (expires_at > created_at)
);

-- For whoever sweeps expired rows; no read in this module uses it.
CREATE INDEX IF NOT EXISTS customer_password_resets_expires_idx
    ON customer_password_resets (expires_at);
