-- Rolling back forgets which browser opened each session; the sessions stay.
ALTER TABLE auth_session
    DROP CONSTRAINT IF EXISTS auth_session_user_agent_bounded,
    DROP COLUMN IF EXISTS user_agent;
