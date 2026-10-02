-- ReviseCustomerContact writes the customer's name and phone only while they
-- are still the ones the caller read (ADR 0337): one conditional UPDATE, so a
-- correction made meanwhile is never written over. The e-mail is not among
-- them.
-- name: ReviseCustomerContact :one
UPDATE customer
SET first_name = sqlc.arg('first_name')::text,
    last_name  = sqlc.arg('last_name')::text,
    phone      = sqlc.arg('phone')::text,
    updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
  AND first_name = sqlc.arg('read_first_name')::text
  AND last_name = sqlc.arg('read_last_name')::text
  AND phone = sqlc.arg('read_phone')::text
RETURNING *;
