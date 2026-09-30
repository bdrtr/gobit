-- A session names the browser that opened it (ADR 0276).
--
-- user_agent is what the browser said it was when the person signed in, so a
-- person looking at their sessions can tell the laptop from the phone. It is
-- the browser's own word and is only a label: nothing decides anything on it.
ALTER TABLE auth_session
    ADD COLUMN IF NOT EXISTS user_agent TEXT NOT NULL DEFAULT '';

ALTER TABLE auth_session
    ADD CONSTRAINT auth_session_user_agent_bounded CHECK (length(user_agent) <= 512);
