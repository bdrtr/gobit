-- The credentials this module owns.
--
-- It is a table of its OWN and not a column on the customer, and that is the
-- separation the whole module exists to keep: gobit's customer record is a
-- commercial fact and a password is an authentication fact. An installation
-- that drops this module drops this table and keeps every customer.
--
-- The customer_id is NOT a foreign key. gobit's modules do not point at each
-- other's tables (Principle 2.2) and this module is further out than any of
-- them: it lives in a separate Go module and may be installed against a
-- database whose customer table it must not constrain.
CREATE TABLE IF NOT EXISTS customer_credentials (
    customer_id   TEXT        PRIMARY KEY,
    -- The e-mail is what a sign-in names, and it is stored FOLDED.
    --
    -- Addresses are compared case-insensitively by everybody who sends mail, so
    -- two rows differing only in case would be two accounts one person cannot
    -- tell apart. Folding on the way in makes the UNIQUE constraint mean what a
    -- reader thinks it means.
    email         TEXT        NOT NULL UNIQUE,
    -- The argon2id hash, parameters and salt included: a credential keeps the
    -- cost it was written with, so raising the cost invalidates nobody.
    password_hash TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- A hash column holding something that is not a hash is the one corruption
    -- this table can check for itself, and the cheapest moment to refuse it is
    -- the write.
    CONSTRAINT customer_credentials_hash_is_argon2id
        CHECK (password_hash LIKE '$argon2id$%'),
    -- A folded address is what the column promises; a row breaking it would
    -- make the UNIQUE constraint above stop meaning one account per person.
    CONSTRAINT customer_credentials_email_is_folded
        CHECK (email = lower(email)),
    CONSTRAINT customer_credentials_email_not_blank
        CHECK (length(btrim(email)) > 0)
);
