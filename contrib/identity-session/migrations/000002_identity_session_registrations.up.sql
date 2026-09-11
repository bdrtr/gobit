-- A registration that is waiting for its address to be proven.
--
-- A row here is NOT an account. Nothing about the person exists in the shop yet:
-- no customer record, no credential, no session. That is the whole point — an
-- address typed into a form is a claim, and creating a customer for every claim
-- lets anybody fill the shop's customer table with addresses that are not theirs.
CREATE TABLE IF NOT EXISTS customer_registrations (
    -- The SHA-256 of the token that was sent, hex. The token itself is never
    -- stored: a leaked table has to be useless, and a table holding the tokens
    -- would be a table of working sign-up links for addresses the leaker does
    -- not control.
    token_hash    TEXT        PRIMARY KEY,
    -- The address being proven, folded to lower case.
    --
    -- UNIQUE, so asking again REPLACES the pending registration rather than
    -- adding one. A person who did not get the message asks again and expects the
    -- newest link to work; and without it one address could accumulate rows.
    email         TEXT        NOT NULL UNIQUE,
    -- The argon2id hash of the password chosen at registration.
    --
    -- Hashed HERE rather than at verification, because the plaintext must not
    -- outlive the request it arrived in — and the person does not send it again
    -- when they click the link.
    password_hash TEXT        NOT NULL,
    -- When the link stops working.
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT customer_registrations_hash_is_argon2id
        CHECK (password_hash LIKE '$argon2id$%'),
    CONSTRAINT customer_registrations_email_is_folded
        CHECK (email = lower(email)),
    CONSTRAINT customer_registrations_email_not_blank
        CHECK (length(btrim(email)) > 0),
    -- A row that is already expired when it is written is a bug in the caller,
    -- and the cheapest place to refuse it is here.
    CONSTRAINT customer_registrations_expires_after_creation
        CHECK (expires_at > created_at)
);

-- Expired rows are swept by whoever runs the shop, not by this module: a DELETE
-- on a schedule is an operator's decision. The index makes that sweep cheap and
-- is not used by any read in this module, which takes rows by primary key.
CREATE INDEX IF NOT EXISTS customer_registrations_expires_idx
    ON customer_registrations (expires_at);
