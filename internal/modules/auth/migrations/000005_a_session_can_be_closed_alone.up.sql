-- A session can be closed alone (ADR 0267).
--
-- A session token used to carry no state: logging out moved the identity's
-- anchor and dropped EVERY token signed before it. Each sign-in now writes a
-- row, the token names it, and a row closed by itself closes that one token
-- and no other. The anchor stays: logging out everywhere and changing the
-- password still close everything, and a token signed before this table
-- existed carries no row and is judged by the anchor alone until it expires.
CREATE TABLE IF NOT EXISTS auth_session (
    id         TEXT        PRIMARY KEY,
    user_id    TEXT        NOT NULL REFERENCES auth_user (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    -- revoked_at is set when this one session is closed; closing every
    -- session moves the anchor and leaves it NULL (the listing applies the
    -- anchor, as the verification does).
    revoked_at TIMESTAMPTZ,

    CONSTRAINT auth_session_expires_after_it_begins CHECK (expires_at > created_at),
    CONSTRAINT auth_session_closed_after_it_begins CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

-- A person's sessions, newest first: the listing and the pruning at sign-in.
CREATE INDEX IF NOT EXISTS auth_session_user_idx ON auth_session (user_id, created_at DESC);
