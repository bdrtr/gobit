-- ReviseCustomerGroup writes the group's name and rank only while they are
-- the ones the caller read (ADR 0329): an operator renaming a group another
-- operator renamed first is told so rather than undoing the other.
-- name: ReviseCustomerGroup :one
UPDATE customer_group
SET name       = sqlc.arg('name')::text,
    rank       = sqlc.arg('rank')::int,
    updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id')
  AND name = sqlc.arg('read_name')::text
  AND rank = sqlc.arg('read_rank')::int
  AND deleted_at IS NULL
RETURNING *;
