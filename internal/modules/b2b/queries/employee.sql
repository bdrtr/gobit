-- b2b_company_employee queries. Every read applies the deleted_at IS NULL
-- filter.
--
-- customer_id DOES NOT APPEAR in this file: the tie between an employee and a
-- customer is not in the schema but in core/link (see
-- migrations/000001_b2b_init.up.sql).

-- name: InsertEmployee :one
INSERT INTO b2b_company_employee (
    id, company_id, spending_limit, is_company_admin, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $5)
RETURNING *;

-- name: GetEmployee :one
SELECT * FROM b2b_company_employee
WHERE id = $1 AND deleted_at IS NULL;

-- ListEmployees returns the filtered and paginated list of employees.
-- name: ListEmployees :many
SELECT * FROM b2b_company_employee
WHERE deleted_at IS NULL
  AND (sqlc.narg('company_id')::text IS NULL OR company_id = sqlc.narg('company_id')::text)
  AND (sqlc.narg('is_company_admin')::boolean IS NULL
       OR is_company_admin = sqlc.narg('is_company_admin')::boolean)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountEmployees :one
SELECT count(*) FROM b2b_company_employee
WHERE deleted_at IS NULL
  AND (sqlc.narg('company_id')::text IS NULL OR company_id = sqlc.narg('company_id')::text)
  AND (sqlc.narg('is_company_admin')::boolean IS NULL
       OR is_company_admin = sqlc.narg('is_company_admin')::boolean);

-- UpdateEmployee leaves the fields it was not given EXACTLY AS THEY WERE.
--
-- COALESCE CANNOT BE USED for spending_limit: the field itself may be NULL
-- ("unlimited") and COALESCE could not tell a "make it unlimited" request apart
-- from "do not touch it". The distinction is carried by a separate flag: if
-- clear_limit is true the column is pulled to NULL, otherwise the given value
-- is written or the old value is kept.
-- name: UpdateEmployee :one
UPDATE b2b_company_employee SET
    spending_limit   = CASE
        WHEN sqlc.arg('clear_limit')::boolean THEN NULL
        ELSE COALESCE(sqlc.narg('spending_limit')::bigint, spending_limit)
    END,
    is_company_admin = COALESCE(sqlc.narg('is_company_admin')::boolean, is_company_admin),
    updated_at       = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteEmployee :one
UPDATE b2b_company_employee
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- SoftDeleteEmployeesOfCompany soft-deletes all of the company's employees and
-- returns THE IDS OF THE DELETED ONES.
--
-- Returning the ids is mandatory: the employee's tie to a customer is in the
-- link table, and if that tie is not deleted the customer can never again be
-- added as an employee to ANY company, because of the cardinality constraint
-- (see service.Definitions, OneToOne).
-- name: SoftDeleteEmployeesOfCompany :many
UPDATE b2b_company_employee
SET deleted_at = $2, updated_at = $2
WHERE company_id = $1 AND deleted_at IS NULL
RETURNING id;
