-- ReviseMethodValue writes the discount's value only while the method is
-- still of the type and the value the caller read (ADR 0338): one
-- conditional UPDATE, so a change made meanwhile is never written over. The
-- type, the target, the allocation, the quantities and the currency are not
-- among what it writes.
-- name: ReviseMethodValue :one
UPDATE promotion_application_method
SET value      = sqlc.arg('value')::bigint,
    updated_at = sqlc.arg('updated_at')
WHERE promotion_id = sqlc.arg('promotion_id')
  AND deleted_at IS NULL
  AND type = sqlc.arg('read_type')::text
  AND value = sqlc.arg('read_value')::bigint
RETURNING *;
