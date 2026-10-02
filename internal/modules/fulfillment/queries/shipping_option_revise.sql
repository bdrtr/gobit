-- ReviseShippingOption writes the option's name, fee and storefront
-- visibility only while they are still the ones the caller read (ADR 0333):
-- one conditional UPDATE, so a revision made meanwhile is never written over.
--
-- The provider, the profile, the price type, the region and the provider's
-- configuration are not among them. A calculated option takes no fee of its
-- own: shipping_options_calculated_zero refuses one, and the repository tells
-- the operator so in the words it tells a new option's.
-- name: ReviseShippingOption :one
UPDATE shipping_options
SET name       = sqlc.arg('name')::text,
    amount     = sqlc.arg('amount')::bigint,
    admin_only = sqlc.arg('admin_only')::boolean,
    updated_at = now()
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
  AND name = sqlc.arg('read_name')::text
  AND amount = sqlc.arg('read_amount')::bigint
  AND admin_only = sqlc.arg('read_admin_only')::boolean
RETURNING *;
