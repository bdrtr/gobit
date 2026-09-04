-- auth_identity queries. Every read applies the deleted_at IS NULL filter.
--
-- CAUTION: password_hash is in the result set of these queries and NEVER
-- LEAVES the repository layer; the service hands it to the bcrypt comparison
-- only, and puts it in no log line and no error message.
--
-- # updated_at is a SECURITY ANCHOR in this table
--
-- Here the column does NOT mean "when was the row last written": it moves
-- forward only on the two operations the account owner performs DELIBERATELY —
-- a password change (UpdatePasswordHash) and a logout (RevokeSessions).
-- Session revocation rests on it: the service rejects session tokens produced
-- before that moment (see service/session.go, sessionAnchor).
--
-- That is why the queries writing the login counters (RegisterLoginFailure,
-- RegisterLoginSuccess) DO NOT TOUCH updated_at. Had they touched it:
--
--   * a single FAILED login attempt would drop every session an administrator
--     had; an attacker would only need to know the email address, and would
--     hold a targeted denial-of-service tool,
--   * logging in from a second device would close the first device's session.
--
-- The same distinction is already present in this schema: api_key.last_used_at
-- does not move updated_at either (see api_keys.sql, MarkAPIKeyUsed). Usage
-- and attempt counters are telemetry, not the content of the record.

-- name: InsertIdentity :one
INSERT INTO auth_identity (
    id, user_id, provider, provider_identity, password_hash, metadata, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
RETURNING *;

-- name: GetIdentityOfUser :one
SELECT * FROM auth_identity
WHERE user_id = $1 AND provider = $2 AND deleted_at IS NULL;

-- UpdatePasswordHash changes the password and RESETS the lockout counters.
--
-- The reset is essential: a user who changes their password must not run into
-- the lock left behind by the failed attempts made with the old password.
--
-- Moving updated_at forward is essential too: it is the only thing that drops
-- the EXISTING session tokens (the "security anchor" note at the top of this
-- file). Had the password changed while the anchor stayed where it was, a
-- leaked token would remain valid until it expired.
-- name: UpdatePasswordHash :one
UPDATE auth_identity SET
    password_hash   = $2,
    failed_attempts = 0,
    locked_until    = NULL,
    updated_at      = $3
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- RevokeSessions moves the session anchor forward; it DOES NOT TOUCH THE
-- CREDENTIAL.
--
-- The whole of logging out is this single write. The token holds no state and
-- there is no jti-based blocklist, so an operation that says "drop that token"
-- CANNOT be performed; the only thing that can be done is to move the anchor
-- forward, that is, to invalidate every token produced before it at once (see
-- service/session.go).
--
-- # NO provider is SELECTED: ALL of the user's identities are moved forward
--
-- The filter is user_id alone. This table keeps a row PER provider (the
-- (user_id, provider) uniqueness), and had a single provider been selected,
-- then the day OAuth is added logging out would not drop the tokens taken from
-- that provider — and it would fail SILENTLY: the endpoint returns 200 and the
-- user who says "I logged out" would still be in session. Today, with a single
-- provider, the number of affected rows is one and the observable behavior is
-- the same; what changes is that the day the second provider is added LEAVES
-- NO silent hole.
--
-- The read side applies the same rule: when a token is verified the anchor is
-- read from the user's MOST RECENT identity rather than from one single
-- provider (see GetSessionAnchor). Had the two diverged, the write here would
-- be wasted.
--
-- password_hash IS NOT TOUCHED: logging out does not change the password, and
-- if it did the user could never log in again.
--
-- failed_attempts and locked_until ARE NOT TOUCHED either. Had they been
-- reset, the logout endpoint would become the way to clear the lock: if a
-- locked account still holds a valid token (a lock does not drop tokens), the
-- counter could be reset forever by repeating "log out + try again", so the
-- lock would never engage at all.
--
-- If the user has no live identity at all, NO row is returned and the caller
-- turns that into errors.NotFound; returning success silently would present a
-- logout that dropped nothing as a success.
-- name: RevokeSessions :many
UPDATE auth_identity SET
    updated_at = $2
WHERE user_id = $1 AND deleted_at IS NULL
RETURNING *;

-- GetSessionAnchor returns the user's MOST RECENT session anchor.
--
-- Token verification rests on this single value: if "iat" is earlier than it,
-- the token is rejected (see service/interop.go, principalFromToken).
--
-- # Why the MOST RECENT (and why not a single provider)
--
-- There is NO claim saying which provider the token was taken from; with no
-- such claim, the anchor cannot be selected by provider. Two extremes can be
-- selected, and taking the OLDEST would be wrong: a single row whose anchor
-- never moves (e.g. a password change writes only the emailpass row) would
-- render the whole revocation ineffective. The MOST RECENT one is taken — the
-- ambiguity is resolved in favor of security, and the price is that a
-- revocation on one provider drops the other's tokens as well.
--
-- A user's providers can be counted on one hand; the ordering runs over the
-- handful of rows coming from the index scanned by the user_id prefix
-- (auth_identity_user_provider_uniq).
--
-- If there is no live identity at all no row is returned: the caller turns
-- that into errors.NotFound and rejects the token, because no value is left
-- that could say when the token became invalid.
-- name: GetSessionAnchor :one
SELECT updated_at FROM auth_identity
WHERE user_id = $1 AND deleted_at IS NULL
ORDER BY updated_at DESC
LIMIT 1;

-- RegisterLoginFailure counts a failed login attempt ATOMICALLY.
--
-- Why the counter is incremented in SQL: were the number read and written back
-- on the Go side, hundreds of requests sent at the same time would all read
-- "0" and all write "1", and the lock would never engage. The FOR UPDATE row
-- in the CTE locks until the end of the transaction, so the increments are
-- serialized.
--
-- An EXPIRED lock counts as the start of a new window and the counter goes
-- back to 1: a user who waited the lock out and tries again must not be locked
-- out again on a single mistake.
--
-- On an ACTIVE lock this query is NEVER called; the service rejects the
-- request earlier. That is why the "locked_until = NULL if below the
-- threshold" branch cannot erase an active lock.
--
-- updated_at IS NOT TOUCHED: a failed attempt must not drop the victim's open
-- sessions (the "security anchor" note at the top of this file).
-- name: RegisterLoginFailure :one
WITH sonraki AS (
    SELECT
        k.id AS kimlik,
        CASE
            WHEN k.locked_until IS NOT NULL AND k.locked_until <= sqlc.arg('now')::timestamptz THEN 1
            ELSE k.failed_attempts + 1
        END AS deneme
    FROM auth_identity k
    WHERE k.id = sqlc.arg('id') AND k.deleted_at IS NULL
    FOR UPDATE
)
UPDATE auth_identity AS i SET
    failed_attempts = sonraki.deneme,
    locked_until    = CASE
        WHEN sonraki.deneme >= sqlc.arg('threshold')::int THEN sqlc.arg('locked_until')::timestamptz
        ELSE NULL::timestamptz
    END
FROM sonraki
WHERE i.id = sonraki.kimlik
RETURNING i.*;

-- RegisterLoginSuccess clears the counters on a successful login.
--
-- updated_at IS NOT TOUCHED: a new login must not close the user's sessions on
-- other devices (the "security anchor" note at the top of this file).
-- name: RegisterLoginSuccess :exec
UPDATE auth_identity SET
    failed_attempts = 0,
    locked_until    = NULL,
    last_login_at   = $2
WHERE id = $1 AND deleted_at IS NULL;
