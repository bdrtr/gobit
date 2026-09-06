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

-- SoftDeleteCollection stamps the collection's deleted_at.
--
-- It is the write that was MISSING until 2026-09-06 (docs/gaps.md D18): every
-- read here has filtered on "deleted_at IS NULL" since the first migration, the
-- handle index was built partial so a deleted collection's handle would not
-- block a new one — and nothing ever set the column, so a collection created by
-- mistake stayed in the merchant's list and on the storefront forever.
-- name: SoftDeleteCollection :execrows
UPDATE product_collection SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- ClearCollectionProducts releases the products of a collection that is being
-- deleted.
--
-- product.collection_id carries "ON DELETE SET NULL", and that clause CAN NEVER
-- FIRE here: a soft delete is an UPDATE and the collection's row stays
-- physically in place, so the database never runs the action the schema
-- promises. Doing it in SQL of our own is the only way the promise holds.
--
-- The rejected alternative was to leave the pointer standing and let the reads
-- hide the collection. It fails in a way the storefront shows: the product
-- listing filters BY collection_id without joining the collection, so products
-- would keep coming back for a collection nobody can see any more, and the
-- product's own record would name a collection that resolves to nothing. The
-- region module answered the identical question the identical way — see
-- ClearRegionCountries in the region module's country.sql, called when a region
-- is deleted.
-- name: ClearCollectionProducts :execrows
UPDATE product SET collection_id = NULL, updated_at = now()
WHERE collection_id = $1 AND deleted_at IS NULL;

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
-- public_only applies the two flags the category table has carried since the
-- first migration and that nothing has read until now: is_active is the
-- merchant's switch for a category that is not ready, and is_internal is for one
-- that exists for operators and was never meant to be browsable. The storefront
-- passes true; the admin surface passes false and sees everything, which is the
-- only way the merchant can turn a category back on.
SELECT * FROM product_category
WHERE deleted_at IS NULL
  AND (sqlc.narg('parent_id')::text IS NULL OR parent_id = sqlc.narg('parent_id')::text)
  AND (NOT sqlc.arg('public_only')::boolean OR (is_active AND NOT is_internal))
ORDER BY rank, created_at DESC, id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountCategories :one
-- The count applies the SAME predicate as the listing. A count taken over a
-- wider set than the page is a storefront asking for pages that never fill.
SELECT count(*) FROM product_category
WHERE deleted_at IS NULL
  AND (sqlc.narg('parent_id')::text IS NULL OR parent_id = sqlc.narg('parent_id')::text)
  AND (NOT sqlc.arg('public_only')::boolean OR (is_active AND NOT is_internal));

-- name: ListCategoriesByIDs :many
SELECT * FROM product_category
WHERE id = ANY($1::text[]) AND deleted_at IS NULL
ORDER BY rank, id;

-- CountChildCategories counts the LIVE children of a category.
--
-- It exists for one caller, the delete, and it is what turns "a category with
-- children cannot be deleted" into a rule instead of an intention.
-- name: CountChildCategories :one
SELECT count(*) FROM product_category
WHERE parent_id = $1 AND deleted_at IS NULL;

-- SoftDeleteCategory stamps the category's deleted_at.
--
-- The write that was missing; see SoftDeleteCollection for what its absence
-- cost. Its guard is CountChildCategories and it is enforced in the service,
-- not here, because a statement that deleted only childless categories would
-- report "no rows" for a category that EXISTS — and the caller could not tell
-- that from a wrong id.
-- name: SoftDeleteCategory :execrows
UPDATE product_category SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

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

-- SoftDeleteTag stamps the tag's deleted_at.
--
-- The tag's bindings in product_tag_map are LEFT WHERE THEY ARE, and that is
-- not an oversight: ListTagsByProductIDs already joins the tag with
-- "t.deleted_at IS NULL", so a deleted tag disappears from every product that
-- carried it without a single map row being touched. That join predicate was
-- written for a state this module could not reach until now.
--
-- The rejected alternative was to delete the map rows in the same transaction.
-- It is more work for the same visible result and it destroys the one thing the
-- soft delete is for: undoing the delete would bring back a tag that is
-- attached to nothing, so an operator who removed the wrong tag could not put
-- the catalog back.
-- name: SoftDeleteTag :execrows
UPDATE product_tag SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

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
