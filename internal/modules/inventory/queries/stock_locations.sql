-- stock_locations queries.
-- Every read applies the deleted_at IS NULL filter (plan Section 8).

-- name: CreateStockLocation :one
INSERT INTO stock_locations (
    id, name, address_1, address_2, city, province, postal_code, country_code
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetStockLocation :one
SELECT * FROM stock_locations
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListStockLocations :many
SELECT * FROM stock_locations
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountStockLocations gives the total for the pagination envelope; it applies
-- the same filter as ListStockLocations.
--
-- The count is a separate query: a window function returned alongside the rows
-- would show the total as 0 on an out-of-range page, because no row comes back
-- there.
-- name: CountStockLocations :one
SELECT COUNT(*) FROM stock_locations
WHERE deleted_at IS NULL;
