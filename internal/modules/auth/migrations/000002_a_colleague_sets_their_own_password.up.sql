-- An invitation waiting for the person it was sent to.
--
-- # What it replaces
--
-- Adding a colleague meant one of two things and both were wrong. Either the
-- creating administrator typed their password and told them — so one person knew
-- another's secret, and the shop's own record of "who can act as this user" was
-- false from the first minute — or the user was created with no password, which
-- writes no auth_identity row at all and leaves an account that cannot log in and
-- nothing anywhere saying why.
--
-- A row here is neither. It is the right to set a FIRST password, held by whoever
-- has the token, for as long as the deadline allows.
CREATE TABLE IF NOT EXISTS auth_user_invitation (
    -- The SHA-256 of the token that was sent, hex. The token itself is never
    -- stored: a leaked table has to be useless, and a table holding the tokens
    -- would be a table of working admin accounts.
    --
    -- The shape is the api_key table's, one file over, for the same reason and
    -- with the same CHECK — that pattern was measured and this one does not get to
    -- invent a second.
    token_hash TEXT        PRIMARY KEY,
    -- The user this invitation opens. ON DELETE CASCADE: an invitation to an
    -- account somebody removed is not a right anybody should still hold.
    user_id    TEXT        NOT NULL REFERENCES auth_user (id) ON DELETE CASCADE,
    -- Who sent it, for the audit trail an operator reads afterwards. It is a user
    -- id and not a name, because names change.
    invited_by TEXT        NOT NULL,
    -- When the link stops working.
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT auth_user_invitation_token_hash_shape CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT auth_user_invitation_inviter_check     CHECK (length(btrim(invited_by)) > 0),
    -- A row that is already expired when it is written is a bug in the caller.
    CONSTRAINT auth_user_invitation_expires_check     CHECK (expires_at > created_at)
);

-- One live invitation per user.
--
-- Asking again REPLACES the previous one rather than adding a second: an
-- administrator who resends because the first message did not arrive expects the
-- newest link to work, and two live links to one account is two chances for
-- whoever finds one.
CREATE UNIQUE INDEX IF NOT EXISTS auth_user_invitation_user_uniq
    ON auth_user_invitation (user_id);

-- Expired rows are swept by whoever runs the shop; the index makes that cheap. No
-- read in this module uses it — they all take the row by its primary key.
CREATE INDEX IF NOT EXISTS auth_user_invitation_expires_idx
    ON auth_user_invitation (expires_at);
