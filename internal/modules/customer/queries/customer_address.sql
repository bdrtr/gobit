-- customer_address queries. Every read filters on deleted_at IS NULL.

-- name: InsertCustomerAddress :one
INSERT INTO customer_address (
    id, customer_id, first_name, last_name, company,
    address_1, address_2, city, country_code, postal_code, phone,
    is_default_shipping, is_default_billing, created_at, updated_at, province
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14, $15)
RETURNING *;

-- GetCustomerAddress reads the address by its id AND ITS OWNER.
--
-- The customer_id condition is deliberate: had the ownership check been left
-- outside the query, a request that guesses a customer's address id could read
-- someone else's address. As long as the check is in the WHERE, it cannot be
-- skipped.
-- name: GetCustomerAddress :one
SELECT * FROM customer_address
WHERE id = $1 AND customer_id = $2 AND deleted_at IS NULL;

-- name: ListCustomerAddresses :many
SELECT * FROM customer_address
WHERE customer_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC;

-- name: UpdateCustomerAddress :one
UPDATE customer_address SET
    first_name   = COALESCE(sqlc.narg('first_name')::text, first_name),
    last_name    = COALESCE(sqlc.narg('last_name')::text, last_name),
    company      = COALESCE(sqlc.narg('company')::text, company),
    address_1    = COALESCE(sqlc.narg('address_1')::text, address_1),
    address_2    = COALESCE(sqlc.narg('address_2')::text, address_2),
    city         = COALESCE(sqlc.narg('city')::text, city),
    province     = COALESCE(sqlc.narg('province')::text, province),
    country_code = COALESCE(sqlc.narg('country_code')::text, country_code),
    postal_code  = COALESCE(sqlc.narg('postal_code')::text, postal_code),
    phone        = COALESCE(sqlc.narg('phone')::text, phone),
    updated_at   = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND customer_id = sqlc.arg('customer_id') AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteCustomerAddress :one
UPDATE customer_address
SET deleted_at = $3, updated_at = $3
WHERE id = $1 AND customer_id = $2 AND deleted_at IS NULL
RETURNING id;

-- ClearDefaultShipping removes the customer's default shipping flag.
--
-- It has to run BEFORE the new default is written: the partial unique index
-- allows one flagged row per customer, and if the clearing is skipped the
-- second marking comes back with a uniqueness violation.
-- name: ClearDefaultShipping :exec
UPDATE customer_address
SET is_default_shipping = FALSE, updated_at = $2
WHERE customer_id = $1 AND is_default_shipping AND deleted_at IS NULL;

-- name: MarkDefaultShipping :one
UPDATE customer_address
SET is_default_shipping = TRUE, updated_at = $3
WHERE id = $1 AND customer_id = $2 AND deleted_at IS NULL
RETURNING *;

-- name: ClearDefaultBilling :exec
UPDATE customer_address
SET is_default_billing = FALSE, updated_at = $2
WHERE customer_id = $1 AND is_default_billing AND deleted_at IS NULL;

-- name: MarkDefaultBilling :one
UPDATE customer_address
SET is_default_billing = TRUE, updated_at = $3
WHERE id = $1 AND customer_id = $2 AND deleted_at IS NULL
RETURNING *;

-- ListAddressesForDisclosure reads ALL the addresses of the given customers.
--
-- It is the one query in this file without deleted_at, and the exception rests
-- on the same fact as the one on the erasure path: a soft delete writes only
-- deleted_at and updated_at, so a deleted address row carries the person's
-- street, door number and phone UNCHANGED. The answer to "what do you hold
-- about us" is not the rows that show up in lists but the rows the database
-- REALLY holds.
--
-- customer_address_customer_idx CANNOT SERVE this query: the index is a
-- partial index built WHERE deleted_at IS NULL, and the query goes outside its
-- condition. The scan is accepted on the same grounds accepted on the erasure
-- path (see repository/erasure.go, lockErasureTargets): a disclosure request
-- runs a few times per person per lifetime and is on no request path a
-- customer waits on.
--
-- The addresses of ALL the customers are asked for in one call; a subject
-- resolved by e-mail can reach dozens of guest records, and a separate query
-- for each would tie the file's cost to the number of orders the person has
-- placed in the past.
--
-- The ordering is for determinism and keeps the per-customer breakdown: a file
-- produced twice for the same subject has to show the rows in the same order.
-- name: ListAddressesForDisclosure :many
SELECT * FROM customer_address
WHERE customer_id = ANY(@customer_ids::text[])
ORDER BY customer_id, created_at DESC, id DESC;
