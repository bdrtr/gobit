-- Schema of the auth module (plan Phase 8, Section 6).
--
-- The tables belong ONLY to this module. Per Principle 2.2 no REFERENCES is
-- given to another module's table; foreign keys BETWEEN the module's OWN tables
-- are free and are used.
--
-- Time columns are TIMESTAMPTZ and are always written in UTC; deletion is SOFT
-- (deleted_at) and every read query applies the deleted_at IS NULL filter.
--
-- SECURITY NOTE: two columns in this schema carry a secret and both store a
-- HASH, plain text is NEVER written: auth_identity.password_hash (bcrypt) and
-- api_key.token_hash (SHA-256). Their rationales are above the tables concerned.

-- auth_user is an ADMINISTRATION user (the person who logs in to the admin
-- panel).
--
-- The table is NOT named "user": in PostgreSQL user is a reserved keyword and
-- would have to be quoted in every query.
--
-- It must not be confused with the customer: the person shopping in the store
-- is data of the customer module, whereas the record here is the staff member
-- who accesses the administration surface. Keeping the two concepts in separate
-- modules is deliberate; there is no path by which a customer gains admin
-- privileges.
--
-- THE PASSWORD IS NOT HERE: the authentication method is kept in the
-- auth_identity table (the rationale is there).
CREATE TABLE IF NOT EXISTS auth_user (
    id         TEXT PRIMARY KEY,
    email      TEXT        NOT NULL,
    first_name TEXT        NOT NULL DEFAULT '',
    last_name  TEXT        NOT NULL DEFAULT '',
    avatar_url TEXT        NOT NULL DEFAULT '',
    scopes     TEXT[]      NOT NULL DEFAULT ARRAY['admin']::text[],
    metadata   JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT auth_user_email_check       CHECK (email <> '' AND email = lower(email)),
    CONSTRAINT auth_user_email_shape_check CHECK (email ~ '^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$'),
    CONSTRAINT auth_user_email_len_check   CHECK (length(email) <= 320),
    CONSTRAINT auth_user_scopes_check      CHECK (array_position(scopes, '') IS NULL)
);

-- The e-mail is unique among LIVE users.
--
-- Uniqueness is a precondition of the login flow: if there were two matching
-- rows for a user logging in with "e-mail + password", login could not know
-- which identity to hand out. The deleted_at IS NULL condition leaves a deleted
-- user's e-mail reusable.
CREATE UNIQUE INDEX IF NOT EXISTS auth_user_email_uniq
    ON auth_user (email)
    WHERE deleted_at IS NULL;

-- auth_identity is a SINGLE authentication method of a user.
--
-- Why separate from auth_user: a user can have more than one way to log in.
-- Today there is only "emailpass" (e-mail + password); tomorrow, when OAuth is
-- added, a second row is attached to the SAME user and the user record is not
-- touched. Had the password column been on auth_user, a user without a password
-- (OAuth only) could not be expressed, or would be represented with an empty
-- password.
--
-- password_hash is BCRYPT output; the plain password is NEVER written, NEVER
-- logged. The bcrypt cost parameter is stored INSIDE the hash: when the cost is
-- raised later, old hashes keep being verified with their own cost. On an
-- identity that has no password (e.g. OAuth only) the column is empty and login
-- is REFUSED.
CREATE TABLE IF NOT EXISTS auth_identity (
    id                TEXT PRIMARY KEY,
    user_id           TEXT        NOT NULL REFERENCES auth_user(id) ON DELETE CASCADE,
    provider          TEXT        NOT NULL,
    provider_identity TEXT        NOT NULL,
    password_hash     TEXT        NOT NULL DEFAULT '',
    failed_attempts   INTEGER     NOT NULL DEFAULT 0,
    locked_until      TIMESTAMPTZ,
    last_login_at     TIMESTAMPTZ,
    metadata          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ,
    CONSTRAINT auth_identity_provider_check  CHECK (provider <> ''),
    CONSTRAINT auth_identity_identity_check  CHECK (provider_identity <> ''),
    CONSTRAINT auth_identity_attempts_check  CHECK (failed_attempts >= 0)
);

-- An identity at one provider is bound to AT MOST ONE user.
--
-- Without the condition the same e-mail could be bound to two users as an
-- "emailpass" identity and login would match two different people.
CREATE UNIQUE INDEX IF NOT EXISTS auth_identity_provider_uniq
    ON auth_identity (provider, provider_identity)
    WHERE deleted_at IS NULL;

-- A user has AT MOST ONE identity at one provider.
--
-- The index above closes the REVERSE direction (an identity at one provider
-- cannot be bound to two users) but does not prevent a SECOND row from being
-- opened for the same user from the same provider. The code assumes this does
-- not happen: the identity is read as a SINGLE row by (user_id, provider), and
-- password verification, the failed-attempt counter and the lock are always
-- written to that row. Were there two rows, which one is read would be left to
-- the query's ordering: a password change is written to only one of them, the
-- other stays open with the old password, and because the attempt counter is
-- split in two the lock threshold would silently double.
--
-- The partial condition (deleted_at IS NULL) is there so that a new identity
-- can be opened in place of a deleted one; the other uniqueness rules in the
-- schema follow the same rule.
--
-- A separate user_id index is NOT KEPT: this index can also be searched by the
-- user_id PREFIX, and a second one would only be an extra cost paid on every
-- write.
CREATE UNIQUE INDEX IF NOT EXISTS auth_identity_user_provider_uniq
    ON auth_identity (user_id, provider)
    WHERE deleted_at IS NULL;

-- sales_channel is a sales channel (e.g. "Web", "Mobile app", "Dealer").
--
-- Publishable API keys are bound to channels, and a storefront request learns
-- which channel it came from through that binding. Catalog filtering (which
-- product is visible in which channel) is established with the product ↔
-- sales_channel link, and auth never sees that link (Principle 2.2).
CREATE TABLE IF NOT EXISTS sales_channel (
    id          TEXT PRIMARY KEY,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    is_disabled BOOLEAN     NOT NULL DEFAULT FALSE,
    metadata    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,
    CONSTRAINT sales_channel_name_check     CHECK (name <> ''),
    CONSTRAINT sales_channel_name_len_check CHECK (length(name) <= 255)
);

-- The channel name is unique among LIVE channels; so that a deleted name can be
-- reused, the condition is deleted_at IS NULL.
CREATE UNIQUE INDEX IF NOT EXISTS sales_channel_name_uniq
    ON sales_channel (name)
    WHERE deleted_at IS NULL;

-- api_key is a machine identity; it has two KINDS and they are NOT the same
-- thing.
--
--   secret      — the SECRET that accesses the administration surface. It is
--                 kept on the server, never handed to the browser; its leak
--                 means admin access.
--   publishable — NOT A SECRET. It is visible in the browser, its only job is
--                 to bind the request to a sales channel; it carries no
--                 privilege.
--
-- The key ITSELF is not stored, only token_hash (SHA-256, hex) is stored.
-- Why not bcrypt: the key is a 256-bit random string that we generate ourselves,
-- not a human password open to a dictionary attack; what the slow hash protects
-- against (offline brute force) is already impossible here. In return, this hash
-- is computed on EVERY REQUEST and bcrypt would add ~250 ms to every admin
-- request. Moreover bcrypt's per-row salt would require scanning the WHOLE table
-- to find the key; SHA-256 is a single, indexable lookup.
--
-- created_by is the identity of whoever generated the key and it CARRIES NO
-- foreign key: the value can be a user identifier ("user_…") just as well as the
-- identifier of another secret key ("apikey_…"), that is, it does not point at a
-- single table.
CREATE TABLE IF NOT EXISTS api_key (
    id           TEXT PRIMARY KEY,
    type         TEXT        NOT NULL,
    title        TEXT        NOT NULL,
    token_hash   TEXT        NOT NULL,
    redacted     TEXT        NOT NULL,
    scopes       TEXT[]      NOT NULL DEFAULT '{}'::text[],
    created_by   TEXT        NOT NULL DEFAULT '',
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    revoked_by   TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,
    CONSTRAINT api_key_type_check       CHECK (type IN ('publishable', 'secret')),
    CONSTRAINT api_key_title_check      CHECK (title <> ''),
    CONSTRAINT api_key_title_len_check  CHECK (length(title) <= 255),
    CONSTRAINT api_key_token_hash_check CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT api_key_scopes_check     CHECK (array_position(scopes, '') IS NULL)
);

-- token_hash is unique and this index is NOT PARTIAL.
--
-- Had a deleted_at or revoked_at condition been added, the hash of a revoked key
-- would become reusable. Since the key is 256 random bits, a collision is
-- practically impossible; the index is therefore not a constraint but an alarm
-- that shows INSTANTLY that the generator is broken.
CREATE UNIQUE INDEX IF NOT EXISTS api_key_token_hash_uniq
    ON api_key (token_hash);

CREATE INDEX IF NOT EXISTS api_key_type_idx
    ON api_key (type)
    WHERE deleted_at IS NULL;

-- api_key_sales_channel is the MANY-TO-MANY link between a publishable key and
-- a sales channel.
--
-- The composite primary key prevents the same link from being established
-- twice; the link is a set, it carries no multiplicity. Since both tables belong
-- to this module the foreign key is free (Principle 2.2 forbids only
-- CROSS-MODULE FKs).
CREATE TABLE IF NOT EXISTS api_key_sales_channel (
    api_key_id       TEXT        NOT NULL REFERENCES api_key(id) ON DELETE CASCADE,
    sales_channel_id TEXT        NOT NULL REFERENCES sales_channel(id) ON DELETE CASCADE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (api_key_id, sales_channel_id)
);

-- Listing the keys bound to a channel cannot use the PREFIX of the primary key;
-- the reverse direction needs its own index.
CREATE INDEX IF NOT EXISTS api_key_sales_channel_channel_idx
    ON api_key_sales_channel (sales_channel_id);
