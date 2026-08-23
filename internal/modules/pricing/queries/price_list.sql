-- price_list sorguları.

-- name: InsertPriceList :one
INSERT INTO price_list (
    id, title, description, type, status, starts_at, ends_at, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING *;

-- name: GetPriceList :one
SELECT * FROM price_list
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListPriceLists :many
SELECT * FROM price_list
WHERE deleted_at IS NULL
ORDER BY id
LIMIT $1 OFFSET $2;

-- name: CountPriceLists :one
SELECT count(*) FROM price_list
WHERE deleted_at IS NULL;

-- name: UpdatePriceList :one
UPDATE price_list
SET title       = $2,
    description = $3,
    type        = $4,
    status      = $5,
    starts_at   = $6,
    ends_at     = $7,
    updated_at  = $8
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeletePriceList :one
UPDATE price_list
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;
