-- auth_user queries. Every read applies the deleted_at IS NULL filter.

-- name: InsertUser :one
INSERT INTO auth_user (id, email, first_name, last_name, avatar_url, scopes, metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING *;

-- name: GetUser :one
SELECT * FROM auth_user
WHERE id = $1 AND deleted_at IS NULL;

-- GetUserByEmail returns the LIVE user for an email address.
--
-- It is the first step of the login flow. Uniqueness is guaranteed by a
-- partial index, so the result is at most one row.
-- name: GetUserByEmail :one
SELECT * FROM auth_user
WHERE email = $1 AND deleted_at IS NULL;

-- LockLiveUser verifies that the user is LIVE and locks the row until the end
-- of the transaction.
--
-- Whoever hangs a row off a user reads it through this query, not through
-- GetUser. Why a foreign key is not enough: an FK looks at the PHYSICAL
-- existence of auth_user's row, and a soft-deleted user (deleted_at set)
-- passes it, so the write lands on a user nothing else can see any more.
--
-- FOR SHARE is what makes the check hold until the write. It was MEASURED
-- (2026-09-06) that without it SetPasswordHash writes a LIVE identity under a
-- user deleted in the meantime, and the row is unreachable but not harmless:
-- it holds the deleted user's address in auth_identity_provider_uniq forever,
-- so no new administrator can be opened at that address (the whole account of
-- it is in repository/identity.go, SetPasswordHash).
--
-- The lock is SHARED, not exclusive: two password writes for different users
-- have no reason to wait for one another, and even for the same user the row
-- they contend on is the identity row, not this one. The only flow that must
-- wait is a delete, and SoftDeleteUser is an UPDATE — a row-exclusive lock,
-- which FOR SHARE conflicts with. Two FOR SHAREs do not conflict.
-- name: LockLiveUser :one
SELECT * FROM auth_user
WHERE id = $1 AND deleted_at IS NULL
FOR SHARE;

-- name: ListUsers :many
SELECT * FROM auth_user
WHERE deleted_at IS NULL
  AND (sqlc.narg('email')::text IS NULL OR email = sqlc.narg('email')::text)
  AND (sqlc.narg('scope')::text IS NULL OR sqlc.narg('scope')::text = ANY(scopes))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountUsers :one
SELECT count(*) FROM auth_user
WHERE deleted_at IS NULL
  AND (sqlc.narg('email')::text IS NULL OR email = sqlc.narg('email')::text)
  AND (sqlc.narg('scope')::text IS NULL OR sqlc.narg('scope')::text = ANY(scopes));

-- UpdateUser leaves the fields that were not supplied AS THEY ARE.
--
-- This partial update, written with COALESCE, preserves the distinction
-- between "the field was not sent" and "the field was cleared": a NULL
-- parameter keeps the old value, an empty string is a real clearing.
-- name: UpdateUser :one
UPDATE auth_user SET
    email      = COALESCE(sqlc.narg('email')::text, email),
    first_name = COALESCE(sqlc.narg('first_name')::text, first_name),
    last_name  = COALESCE(sqlc.narg('last_name')::text, last_name),
    avatar_url = COALESCE(sqlc.narg('avatar_url')::text, avatar_url),
    scopes     = COALESCE(sqlc.narg('scopes')::text[], scopes),
    metadata   = COALESCE(sqlc.narg('metadata')::jsonb, metadata),
    updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteUser :one
UPDATE auth_user
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- SoftDeleteIdentitiesOfUser deletes the user's identities as well when the
-- user is deleted.
--
-- A foreign key's ON DELETE CASCADE only runs on a REAL delete; because a soft
-- delete is an UPDATE, it does not take the identities with it on its own. Had
-- a deleted user's live identity been left behind, that user could still log
-- in AFTER BEING DELETED — this would be the module's most expensive silent
-- fault.
-- name: SoftDeleteIdentitiesOfUser :exec
UPDATE auth_identity
SET deleted_at = $2, updated_at = $2
WHERE user_id = $1 AND deleted_at IS NULL;

-- SyncIdentityProviderIdentity updates the login identity as well when the
-- user's email address changes.
--
-- The two sit in separate columns but express the SAME thing: the user's login
-- address. Were they not kept in sync, a user who changed their email address
-- would go on logging in with the old one, and the (provider,
-- provider_identity) uniqueness index would occupy an address nobody uses any
-- more. The call is in the SAME transaction as the user update.
-- name: SyncIdentityProviderIdentity :exec
UPDATE auth_identity
SET provider_identity = $3, updated_at = $4
WHERE user_id = $1 AND provider = $2 AND deleted_at IS NULL;

-- PutInvitation writes an invitation, REPLACING the one that user already had.
--
-- The conflict target is the USER rather than the token: resending is the same
-- administrator asking again, and they expect the newest link to work. The old
-- token stops working in the same statement, which is what keeps one account from
-- accumulating live links.
-- name: PutInvitation :one
INSERT INTO auth_user_invitation (token_hash, user_id, invited_by, expires_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id) DO UPDATE
SET token_hash = EXCLUDED.token_hash,
    invited_by = EXCLUDED.invited_by,
    expires_at = EXCLUDED.expires_at,
    created_at = now()
RETURNING *;

-- TakeInvitation removes an invitation and answers what it held.
--
-- Removing and reading are ONE statement, which is what makes a token single-use:
-- two requests carrying one token cannot both be answered, whatever their timing
-- and whatever the isolation level — the second deletes nothing and returns
-- nothing. A SELECT followed by a DELETE would need a lock to say as much, and this
-- repository has measured what a guard without one is worth under READ COMMITTED
-- (gap D46).
--
-- The expiry is in the same WHERE for the same reason: a row that is too old is
-- simply not a row this statement can take, rather than a rule written twice.
-- name: TakeInvitation :one
DELETE FROM auth_user_invitation
WHERE token_hash = $1 AND expires_at > now()
RETURNING *;

-- PutMFACredential writes an enrolment, replacing any credential the user had.
--
-- ON CONFLICT DO UPDATE and not an INSERT that fails: re-enrolling is the ordinary
-- path for somebody who lost their phone, and it has to make the old secret stop
-- working. The confirmation is RESET with it — a new secret nobody has proven yet
-- is exactly what an unconfirmed credential is.
-- name: PutMFACredential :one
INSERT INTO auth_mfa_credential (user_id, secret)
VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE
SET secret = EXCLUDED.secret, confirmed_at = NULL, created_at = now()
RETURNING *;

-- GetMFACredential reads one user's credential.
-- name: GetMFACredential :one
SELECT * FROM auth_mfa_credential WHERE user_id = $1;

-- ConfirmMFACredential stamps the moment the first correct code arrived.
--
-- The WHERE keeps it to an UNCONFIRMED credential, so a second confirmation of
-- the same enrolment changes nothing and the first moment is the one kept. A
-- stamp that moved would make "when did this person prove their phone" unanswerable.
-- name: ConfirmMFACredential :one
UPDATE auth_mfa_credential
SET confirmed_at = now()
WHERE user_id = $1 AND confirmed_at IS NULL
RETURNING *;
