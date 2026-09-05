-- Product queries.
--
-- Shared rules:
--   * Every read applies the "deleted_at IS NULL" filter (plan Section 8: soft
--     delete).
--   * The listing order (created_at DESC, id DESC) is fixed; the second key keeps
--     two records created in the same millisecond from swapping places between
--     pages.
--   * The database produces the timestamps (now()); that is the single clock
--     source.

-- name: CreateProduct :one
INSERT INTO product (
    id, handle, title, subtitle, description, thumbnail, status,
    is_giftcard, discountable, weight, length, height, width,
    material, origin_country, collection_id, metadata
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12, $13,
    $14, $15, $16, $17
)
RETURNING *;

-- name: GetProduct :one
SELECT * FROM product
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetProductForUpdate :one
-- GetProductForUpdate reads the product WITH A ROW LOCK; it is meaningful only
-- inside a transaction.
--
-- Checking that the owner exists before writing a variant is not enough on its
-- own: because deletion is SOFT, the foreign key does not close the gap, and if a
-- concurrent deletion slips in between the check and the INSERT, a variant whose
-- owner has been deleted comes into being. The lock PUTS the deletion IN SEQUENCE
-- with this transaction: if the deletion comes first, no row is found here; if it
-- comes second, the variant is caught by the deletion's cleanup.
SELECT * FROM product
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: GetProductByHandle :one
SELECT * FROM product
WHERE handle = $1 AND deleted_at IS NULL;

-- ListProducts and CountProducts are NOT HERE; they sit in
-- repository/saleschannel.go as hand-written SQL.
--
-- The reason is not visible from this file alone: for the sales channel filter
-- the two queries carry an EXISTS/NOT EXISTS condition against the link table
-- (link_product_sales_channel), and that table is NOT in this module's
-- migrations — core/link creates its schema at run time. Because sqlc reads the
-- schema from this directory, it refuses generation with "relation does not
-- exist". For the full rationale, and for why the filter is applied in the
-- database, see repository/saleschannel.go.

-- name: ListProductsByIDs :many
SELECT * FROM product
WHERE id = ANY($1::text[]) AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC;

-- name: UpdateProduct :one
-- The COALESCE pattern: a field passed as NULL DOES NOT CHANGE. Its known limit
-- is that setting a field back to NULL (clearing the subtitle, say) cannot be
-- done through this endpoint; the PATCH contract is documented as "a field that
-- is not supplied is preserved".
UPDATE product SET
    handle         = COALESCE(sqlc.narg('handle')::text, handle),
    title          = COALESCE(sqlc.narg('title')::text, title),
    subtitle       = COALESCE(sqlc.narg('subtitle')::text, subtitle),
    description    = COALESCE(sqlc.narg('description')::text, description),
    thumbnail      = COALESCE(sqlc.narg('thumbnail')::text, thumbnail),
    status         = COALESCE(sqlc.narg('status')::text, status),
    discountable   = COALESCE(sqlc.narg('discountable')::boolean, discountable),
    weight         = COALESCE(sqlc.narg('weight')::int, weight),
    length         = COALESCE(sqlc.narg('length')::int, length),
    height         = COALESCE(sqlc.narg('height')::int, height),
    width          = COALESCE(sqlc.narg('width')::int, width),
    material       = COALESCE(sqlc.narg('material')::text, material),
    origin_country = COALESCE(sqlc.narg('origin_country')::text, origin_country),
    collection_id  = COALESCE(sqlc.narg('collection_id')::text, collection_id),
    metadata       = COALESCE(sqlc.narg('metadata')::jsonb, metadata),
    updated_at     = now()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteProduct :execrows
UPDATE product SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteVariantsByProduct :execrows
UPDATE product_variant SET deleted_at = now(), updated_at = now()
WHERE product_id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteOptionsByProduct :execrows
UPDATE product_option SET deleted_at = now(), updated_at = now()
WHERE product_id = $1 AND deleted_at IS NULL;

-- name: ListVariantIDsByProduct :many
-- When a product is deleted the links of its variants (price/inventory) are
-- cleaned up; for that, the IDs are read BEFORE the deletion.
SELECT id FROM product_variant
WHERE product_id = $1 AND deleted_at IS NULL
ORDER BY rank, id;

-- name: CreateImage :one
-- upload_id may be NULL and it is not an error: an image whose address was
-- never uploaded here (an imported catalog, a hand-typed CDN address) has no
-- upload record to point at. See migration 000002.
INSERT INTO product_image (id, product_id, url, rank, metadata, upload_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListImagesByProductIDs :many
SELECT * FROM product_image
WHERE product_id = ANY($1::text[]) AND deleted_at IS NULL
ORDER BY product_id, rank, id;

-- name: ListImagesByIDs :many
-- The reverse read of the image/upload binding ends here: the link service
-- answers "which images use this upload" with IDS, and the records themselves
-- are read in a SINGLE query rather than one per id.
--
-- An id with no live row is simply not returned. That is not an error but the
-- residue the write order accepts: the binding is written before the image, so
-- a create that failed afterwards can leave a link row for an image that never
-- existed (see service/links.go).
SELECT * FROM product_image
WHERE id = ANY($1::text[]) AND deleted_at IS NULL
ORDER BY product_id, rank, id;

-- name: DeleteImagesByProduct :exec
UPDATE product_image SET deleted_at = now(), updated_at = now()
WHERE product_id = $1 AND deleted_at IS NULL;
