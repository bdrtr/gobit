-- ReviseCustomerAddress writes the address's printed fields only while they
-- are still the ones the caller read (ADR 0342): one conditional UPDATE, so a
-- correction made meanwhile is never written over. The address has to be the
-- customer's and live; the default flags are not among the fields.
-- name: ReviseCustomerAddress :one
UPDATE customer_address
SET first_name   = sqlc.arg('first_name')::text,
    last_name    = sqlc.arg('last_name')::text,
    company      = sqlc.arg('company')::text,
    address_1    = sqlc.arg('address_1')::text,
    address_2    = sqlc.arg('address_2')::text,
    city         = sqlc.arg('city')::text,
    country_code = sqlc.arg('country_code')::text,
    postal_code  = sqlc.arg('postal_code')::text,
    phone        = sqlc.arg('phone')::text,
    updated_at   = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id')
  AND customer_id = sqlc.arg('customer_id')
  AND deleted_at IS NULL
  AND first_name = sqlc.arg('read_first_name')::text
  AND last_name = sqlc.arg('read_last_name')::text
  AND company = sqlc.arg('read_company')::text
  AND address_1 = sqlc.arg('read_address_1')::text
  AND address_2 = sqlc.arg('read_address_2')::text
  AND city = sqlc.arg('read_city')::text
  AND country_code = sqlc.arg('read_country_code')::text
  AND postal_code = sqlc.arg('read_postal_code')::text
  AND phone = sqlc.arg('read_phone')::text
RETURNING *;
