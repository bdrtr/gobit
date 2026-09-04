-- Collection, category and tag queries.
--
-- The API surface of these three concepts is deliberately plain (list + create,
-- plan Phase 4): the heart of the catalog is the product and the variant, and the
-- taxonomy groups them.

-- name: CreateCollection :one
INSERT INTO product_collection (id, title, handle, metadata)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetCollection :one
SELECT * FROM product_collection
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetCollectionByHandle :one
SELECT * FROM product_collection
WHERE handle = $1 AND deleted_at IS NULL;

-- name: ListCollections :many
SELECT * FROM product_collection
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountCollections :one
SELECT count(*) FROM product_collection WHERE deleted_at IS NULL;

-- name: CreateCategory :one
INSERT INTO product_category (id, name, handle, description, parent_id, is_active, is_internal, rank)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetCategory :one
SELECT * FROM product_category
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetCategoryByHandle :one
SELECT * FROM product_category
WHERE handle = $1 AND deleted_at IS NULL;

-- name: ListCategories :many
SELECT * FROM product_category
WHERE deleted_at IS NULL
  AND (sqlc.narg('parent_id')::text IS NULL OR parent_id = sqlc.narg('parent_id')::text)
ORDER BY rank, created_at DESC, id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountCategories :one
SELECT count(*) FROM product_category
WHERE deleted_at IS NULL
  AND (sqlc.narg('parent_id')::text IS NULL OR parent_id = sqlc.narg('parent_id')::text);

-- name: ListCategoriesByIDs :many
SELECT * FROM product_category
WHERE id = ANY($1::text[]) AND deleted_at IS NULL
ORDER BY rank, id;

-- name: CreateTag :one
INSERT INTO product_tag (id, value)
VALUES ($1, $2)
RETURNING *;

-- name: GetTagByValue :one
SELECT * FROM product_tag
WHERE value = $1 AND deleted_at IS NULL;

-- name: ListTags :many
SELECT * FROM product_tag
WHERE deleted_at IS NULL
ORDER BY value, id
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountTags :one
SELECT count(*) FROM product_tag WHERE deleted_at IS NULL;

-- name: ListTagsByIDs :many
SELECT * FROM product_tag
WHERE id = ANY($1::text[]) AND deleted_at IS NULL
ORDER BY value, id;

-- name: AddProductTag :exec
INSERT INTO product_tag_map (product_id, tag_id)
VALUES ($1, $2)
ON CONFLICT (product_id, tag_id) DO NOTHING;

-- name: DeleteProductTags :exec
DELETE FROM product_tag_map WHERE product_id = $1;

-- name: ListTagsByProductIDs :many
SELECT m.product_id, t.id, t.value
FROM product_tag_map m
JOIN product_tag t ON t.id = m.tag_id AND t.deleted_at IS NULL
WHERE m.product_id = ANY($1::text[])
ORDER BY m.product_id, t.value, t.id;

-- name: AddProductCategory :exec
INSERT INTO product_category_map (product_id, category_id)
VALUES ($1, $2)
ON CONFLICT (product_id, category_id) DO NOTHING;

-- name: DeleteProductCategories :exec
DELETE FROM product_category_map WHERE product_id = $1;

-- name: ListCategoriesByProductIDs :many
SELECT m.product_id, c.id, c.name, c.handle, c.parent_id, c.rank
FROM product_category_map m
JOIN product_category c ON c.id = m.category_id AND c.deleted_at IS NULL
WHERE m.product_id = ANY($1::text[])
ORDER BY m.product_id, c.rank, c.id;
