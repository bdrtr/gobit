-- The passkeys this module owns.
--
-- One row per CREDENTIAL and not per customer: a person registers a passkey on
-- their phone and another on their laptop, and losing one must not cost them the
-- other. That is most of the reason passkeys are worth having.
CREATE TABLE IF NOT EXISTS passkey_credentials (
    -- The credential's own identifier, base64url of the raw bytes the
    -- authenticator minted. It is the primary key because the authenticator
    -- presents it and nothing else at sign-in.
    credential_id TEXT        PRIMARY KEY,
    -- The customer who owns it. No foreign key: this module lives in a Go module
    -- of its own and may not constrain a table another module owns
    -- (Principle 2.2, and further out than any in-tree module).
    customer_id   TEXT        NOT NULL,
    -- The whole credential as the library serialises it.
    --
    -- A column per field was rejected: the library adds fields between versions
    -- — flags, attestation, extensions — and each one would be a migration
    -- whose only reader is the library that wrote it. What this module needs
    -- from the credential is the id and the owner, and those are columns.
    credential    JSONB       NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The last time this key signed in, for a person deciding which of their
    -- keys to remove. NULL until it is used.
    last_used_at  TIMESTAMPTZ,

    CONSTRAINT passkey_credentials_customer_not_blank
        CHECK (length(btrim(customer_id)) > 0),
    -- A credential that is not an object is a row this module cannot read back,
    -- and the cheapest moment to refuse it is the write.
    CONSTRAINT passkey_credentials_is_an_object
        CHECK (jsonb_typeof(credential) = 'object')
);

-- Sign-in reads by credential id; the LISTING reads by customer, and a person
-- with three keys is the ordinary case rather than the large one.
CREATE INDEX IF NOT EXISTS passkey_credentials_customer_idx
    ON passkey_credentials (customer_id);
