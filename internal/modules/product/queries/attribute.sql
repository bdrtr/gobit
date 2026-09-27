-- Typed product attributes (ADR 0219).

-- name: InsertAttribute :one
INSERT INTO product_attribute (id, handle, title, kind, rank)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListAttributes :many
SELECT * FROM product_attribute
WHERE deleted_at IS NULL
ORDER BY rank, handle
LIMIT $1;

-- name: CountAttributes :one
SELECT count(*) FROM product_attribute WHERE deleted_at IS NULL;

-- name: GetAttribute :one
SELECT * FROM product_attribute WHERE id = $1 AND deleted_at IS NULL;

-- UpdateAttribute changes a definition's title and order; its handle and kind
-- are what its values and every storefront filter were written against.
-- name: UpdateAttribute :one
UPDATE product_attribute
SET title = COALESCE(sqlc.narg('title')::text, title),
    rank = COALESCE(sqlc.narg('rank')::int, rank),
    updated_at = now()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteAttribute :execrows
UPDATE product_attribute SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: InsertAttributeOption :one
INSERT INTO product_attribute_option (id, attribute_id, handle, value, rank)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListAttributeOptions :many
SELECT o.* FROM product_attribute_option o
WHERE o.attribute_id = ANY(sqlc.arg('attribute_ids')::text[]) AND o.deleted_at IS NULL
ORDER BY o.attribute_id, o.rank, o.handle;

-- name: CountAttributeOptions :one
SELECT count(*) FROM product_attribute_option WHERE attribute_id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteAttributeOption :execrows
UPDATE product_attribute_option SET deleted_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: DeleteProductAttributeValues :exec
DELETE FROM product_attribute_value WHERE product_id = $1;

-- name: InsertProductAttributeValue :exec
INSERT INTO product_attribute_value (product_id, attribute_id, option_id, number_value, boolean_value)
VALUES ($1, $2, sqlc.narg('option_id'), sqlc.narg('number_value'), sqlc.narg('boolean_value'));

-- ListProductAttributeValues reads the values of many products whose
-- definition and option still stand, in the definitions' order.
-- name: ListProductAttributeValues :many
SELECT v.product_id, a.id AS attribute_id, a.handle AS attribute_handle, a.title AS attribute_title,
       a.kind, v.option_id, o.handle AS option_handle, o.value AS option_value,
       v.number_value, v.boolean_value
FROM product_attribute_value v
JOIN product_attribute a ON a.id = v.attribute_id AND a.deleted_at IS NULL
LEFT JOIN product_attribute_option o ON o.id = v.option_id
WHERE v.product_id = ANY(sqlc.arg('product_ids')::text[])
  AND (v.option_id IS NULL OR o.deleted_at IS NULL)
ORDER BY v.product_id, a.rank, a.handle, o.rank, o.handle;
