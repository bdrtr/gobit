-- The moment a customer's sessions count from (ADR 0374).
--
-- A session cookie issued before it proves nobody. A password reset, an
-- operator replacing a password and a customer ending their other sessions move
-- it forward; nothing moves it back. NULL is a customer whose every session
-- counts, which is every row this migration finds.
--
-- It is a column of the credential rather than a table of its own: a customer
-- with no password here has nothing a reset replaces, and an erasure of the
-- credential takes the moment with the row.
ALTER TABLE customer_credentials
    ADD COLUMN IF NOT EXISTS sessions_valid_from TIMESTAMPTZ;
