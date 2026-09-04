-- api_key queries. The key's PLAINTEXT appears in no query; only token_hash
-- is stored and searched (rationale: migrations/000001_auth_init.up.sql).

-- name: InsertAPIKey :one
INSERT INTO api_key (
    id, type, title, token_hash, redacted, scopes, created_by, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING *;

-- name: GetAPIKey :one
SELECT * FROM api_key
WHERE id = $1 AND deleted_at IS NULL;

-- GetAPIKeyByHash returns the record matching the incoming key's hash.
--
-- REVOKED keys are NOT filtered out here. The reason for that decision is not
-- to keep a measurable timing difference from opening up between reading the
-- record and rejecting it as "revoked" and never finding it at all — it is so
-- that revocation is a separate and EXPLICIT branch on the service side, one
-- that can be proven by a test. Had the query filtered revocation out, the
-- claim "a revoked key is rejected" would blur into "not found", and the day
-- the filter fell away no test would catch it.
-- name: GetAPIKeyByHash :one
SELECT * FROM api_key
WHERE token_hash = $1 AND deleted_at IS NULL;

-- name: ListAPIKeys :many
SELECT * FROM api_key
WHERE deleted_at IS NULL
  AND (sqlc.narg('key_type')::text IS NULL OR type = sqlc.narg('key_type')::text)
  AND (sqlc.narg('revoked')::boolean IS NULL
       OR (sqlc.narg('revoked')::boolean AND revoked_at IS NOT NULL)
       OR (NOT sqlc.narg('revoked')::boolean AND revoked_at IS NULL))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountAPIKeys :one
SELECT count(*) FROM api_key
WHERE deleted_at IS NULL
  AND (sqlc.narg('key_type')::text IS NULL OR type = sqlc.narg('key_type')::text)
  AND (sqlc.narg('revoked')::boolean IS NULL
       OR (sqlc.narg('revoked')::boolean AND revoked_at IS NOT NULL)
       OR (NOT sqlc.narg('revoked')::boolean AND revoked_at IS NULL));

-- RevokeAPIKey revokes the key.
--
-- The revoked_at IS NULL condition is essential: revoking an already revoked
-- key a second time would be a silent no-op, and the revocation time would
-- DRIFT with that second call — the audit record could not show when the key
-- was really closed. When the condition does not hold no row is returned and
-- the service tells the two cases apart.
-- name: RevokeAPIKey :one
UPDATE api_key SET
    revoked_at = $2,
    revoked_by = $3,
    updated_at = $2
WHERE id = $1 AND deleted_at IS NULL AND revoked_at IS NULL
RETURNING *;

-- MarkAPIKeyUsed updates the moment of last use.
--
-- The threshold in the WHERE condition is deliberate: were this column written
-- on every request, every storefront request on a hot publishable key would
-- try to write the same row and authentication would turn into a write
-- bottleneck. Thanks to the threshold the write happens at most once per
-- window per key; the column's value is APPROXIMATE and is documented as such.
-- name: MarkAPIKeyUsed :exec
UPDATE api_key
SET last_used_at = sqlc.arg('used_at')::timestamptz
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
  AND (last_used_at IS NULL OR last_used_at < sqlc.arg('stale_before')::timestamptz);

-- name: SoftDeleteAPIKey :one
UPDATE api_key
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- LinkAPIKeySalesChannel links a publishable key to a sales channel.
--
-- ON CONFLICT DO NOTHING is the counterpart of the link being a SET:
-- establishing the same link twice is not an error, it is a repeat.
-- name: LinkAPIKeySalesChannel :exec
INSERT INTO api_key_sales_channel (api_key_id, sales_channel_id, created_at)
VALUES ($1, $2, $3)
ON CONFLICT (api_key_id, sales_channel_id) DO NOTHING;

-- name: UnlinkAPIKeySalesChannel :execrows
DELETE FROM api_key_sales_channel
WHERE api_key_id = $1 AND sales_channel_id = $2;

-- ListChannelIDsForKey returns the IDs of the ACTIVE channels the key is
-- linked to.
--
-- Disabled and deleted channels are filtered out here: the storefront identity
-- is built from this list, and a disabled channel's catalog must not be
-- visible.
-- name: ListChannelIDsForKey :many
SELECT l.sales_channel_id FROM api_key_sales_channel l
JOIN sales_channel c ON c.id = l.sales_channel_id
WHERE l.api_key_id = $1 AND c.deleted_at IS NULL AND NOT c.is_disabled
ORDER BY l.sales_channel_id;

-- ListChannelsForKey returns ALL of the channels the key is linked to.
--
-- Disabled channels are included as well: the admin surface must show the link
-- as it is, it must not hide that a channel is disabled.
-- name: ListChannelsForKey :many
SELECT c.* FROM api_key_sales_channel l
JOIN sales_channel c ON c.id = l.sales_channel_id
WHERE l.api_key_id = $1 AND c.deleted_at IS NULL
ORDER BY c.name, c.id;

-- name: DeleteLinksOfAPIKey :exec
DELETE FROM api_key_sales_channel
WHERE api_key_id = $1;
