-- A session a sign-in opened (ADR 0267).

-- name: InsertSession :exec
INSERT INTO auth_session (id, user_id, created_at, expires_at, user_agent)
VALUES ($1, $2, $3, $4, $5);

-- name: GetSession :one
SELECT * FROM auth_session WHERE id = $1;

-- ListUnclosedSessions returns a person's sessions that are neither closed by
-- themselves nor expired, newest first. The anchor is applied by the caller,
-- as the verification applies it.
-- name: ListUnclosedSessions :many
SELECT * FROM auth_session
WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > sqlc.arg('now')
ORDER BY created_at DESC, id DESC;

-- CloseSession closes one of the person's sessions and reports whether one
-- was open.
-- name: CloseSession :execrows
UPDATE auth_session SET revoked_at = sqlc.arg('now')
WHERE id = sqlc.arg('id') AND user_id = sqlc.arg('user_id') AND revoked_at IS NULL;

-- CloseOtherSessions closes every open session of the person but one.
-- name: CloseOtherSessions :execrows
UPDATE auth_session SET revoked_at = sqlc.arg('now')
WHERE user_id = sqlc.arg('user_id') AND id <> sqlc.arg('keep_id') AND revoked_at IS NULL
  AND expires_at > sqlc.arg('now');

-- PruneSessions forgets a person's sessions that expired, at their next
-- sign-in, so the table holds what can still be used and nothing a job has to
-- sweep.
-- name: PruneSessions :exec
DELETE FROM auth_session WHERE user_id = $1 AND expires_at <= sqlc.arg('now');
