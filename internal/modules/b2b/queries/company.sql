-- b2b_company queries. Every read applies the deleted_at IS NULL filter.

-- name: InsertCompany :one
INSERT INTO b2b_company (
    id, name, email, phone, address, city, postal_code, country_code,
    currency_code, spending_limit_reset_period, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
RETURNING *;

-- name: GetCompany :one
SELECT * FROM b2b_company
WHERE id = $1 AND deleted_at IS NULL;

-- ListCompanies returns the filtered and paginated list of companies.
--
-- The e-mail filter may return MORE THAN ONE row: a company e-mail is not
-- unique (the argument is in the table's documentation in the migration).
-- name: ListCompanies :many
SELECT * FROM b2b_company
WHERE deleted_at IS NULL
  AND (sqlc.narg('email')::text IS NULL OR email = sqlc.narg('email')::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountCompanies :one
SELECT count(*) FROM b2b_company
WHERE deleted_at IS NULL
  AND (sqlc.narg('email')::text IS NULL OR email = sqlc.narg('email')::text);

-- UpdateCompany leaves the fields it was not given EXACTLY AS THEY WERE.
--
-- This partial update, written with COALESCE, preserves the distinction between
-- "the field was not sent" and "the field was cleared": a NULL parameter keeps
-- the old value, an empty string is a real clearing. For the address fields
-- that distinction is concrete — a company that has moved must be able to have
-- its old postal code deleted.
-- name: UpdateCompany :one
UPDATE b2b_company SET
    name                        = COALESCE(sqlc.narg('name')::text, name),
    email                       = COALESCE(sqlc.narg('email')::text, email),
    phone                       = COALESCE(sqlc.narg('phone')::text, phone),
    address                     = COALESCE(sqlc.narg('address')::text, address),
    city                        = COALESCE(sqlc.narg('city')::text, city),
    postal_code                 = COALESCE(sqlc.narg('postal_code')::text, postal_code),
    country_code                = COALESCE(sqlc.narg('country_code')::text, country_code),
    currency_code               = COALESCE(sqlc.narg('currency_code')::text, currency_code),
    spending_limit_reset_period = COALESCE(sqlc.narg('reset_period')::text, spending_limit_reset_period),
    updated_at                  = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- SoftDeleteCompany soft-deletes the company.
--
-- Its EMPLOYEES are deleted in the same operation as well (see
-- SoftDeleteEmployeesOfCompany); doing the two in separate calls would, on an
-- error in between, leave employee records with no company behind, and those
-- records would show a deleted company in the storefront.
-- name: SoftDeleteCompany :one
UPDATE b2b_company
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;
