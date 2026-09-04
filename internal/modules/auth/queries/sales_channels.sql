-- sales_channel queries. Every read applies the deleted_at IS NULL filter.

-- name: InsertSalesChannel :one
INSERT INTO sales_channel (id, name, description, is_disabled, metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $6)
RETURNING *;

-- name: GetSalesChannel :one
SELECT * FROM sales_channel
WHERE id = $1 AND deleted_at IS NULL;

-- LockLiveSalesChannel verifies that the channel is LIVE and locks the row
-- until the end of the transaction.
--
-- Why a foreign key alone is not enough: an FK looks at the PHYSICAL existence
-- of the row, and a soft-deleted channel (deleted_at set) passes it. The read
-- queries, however, filter that channel out; even if the link is established
-- the key is BORN DEAD — a publishable key that could not be attached to any
-- channel cannot build a storefront identity, and the fault would come to
-- light only on the first request, in the shape of "the channel was linked but
-- it does not work".
--
-- FOR SHARE is deliberate: in the time between the condition being queried and
-- the link being written, it was possible for the channel to be soft-deleted.
-- The shared lock makes the UPDATE that performs the deletion wait until the
-- transaction ends; it does not block writing the link, because two link
-- writes do not collide on each other's lock.
-- name: LockLiveSalesChannel :one
SELECT id FROM sales_channel
WHERE id = $1 AND deleted_at IS NULL
FOR SHARE;

-- name: ListSalesChannels :many
SELECT * FROM sales_channel
WHERE deleted_at IS NULL
  AND (sqlc.narg('name')::text IS NULL OR name = sqlc.narg('name')::text)
  AND (sqlc.narg('is_disabled')::boolean IS NULL OR is_disabled = sqlc.narg('is_disabled')::boolean)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountSalesChannels :one
SELECT count(*) FROM sales_channel
WHERE deleted_at IS NULL
  AND (sqlc.narg('name')::text IS NULL OR name = sqlc.narg('name')::text)
  AND (sqlc.narg('is_disabled')::boolean IS NULL OR is_disabled = sqlc.narg('is_disabled')::boolean);

-- name: ListSalesChannelsByIDs :many
SELECT * FROM sales_channel
WHERE id = ANY(@ids::text[]) AND deleted_at IS NULL
ORDER BY id;

-- name: UpdateSalesChannel :one
UPDATE sales_channel SET
    name        = COALESCE(sqlc.narg('name')::text, name),
    description = COALESCE(sqlc.narg('description')::text, description),
    is_disabled = COALESCE(sqlc.narg('is_disabled')::boolean, is_disabled),
    metadata    = COALESCE(sqlc.narg('metadata')::jsonb, metadata),
    updated_at  = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteSalesChannel :one
UPDATE sales_channel
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- DeleteLinksOfSalesChannel removes the key links as well when a channel is
-- deleted.
--
-- A foreign key's ON DELETE CASCADE only runs on a REAL delete; because a soft
-- delete is an UPDATE, the link rows would stay where they are. If the links
-- are not deleted, then when a deleted channel is reopened under the same name
-- the old keys could silently be taken to be linked to the new channel — the
-- link row keys on the ID, and even though the ID is new, writing this down
-- keeps the reason the link is removed from being forgotten.
-- name: DeleteLinksOfSalesChannel :exec
DELETE FROM api_key_sales_channel
WHERE sales_channel_id = $1;
