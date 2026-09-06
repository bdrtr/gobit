-- Variant and option queries.
--
-- The batch (ByIDs) queries are no coincidence: both the Query provider's
-- FetchByIDs and the variant/option hydration of the product listing have to work
-- with a SINGLE query; one query per record would produce N+1.

-- name: CreateVariant :one
INSERT INTO product_variant (
    id, product_id, title, sku, barcode, ean, upc,
    manage_inventory, allow_backorder, weight, rank, metadata
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12
)
RETURNING *;

-- name: GetVariant :one
SELECT * FROM product_variant
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetVariantBySKU :one
SELECT * FROM product_variant
WHERE sku = $1 AND deleted_at IS NULL;

-- name: ListVariants :many
SELECT * FROM product_variant
WHERE deleted_at IS NULL
  AND (sqlc.narg('product_id')::text IS NULL OR product_id = sqlc.narg('product_id')::text)
ORDER BY product_id, rank, id
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountVariants :one
SELECT count(*) FROM product_variant
WHERE deleted_at IS NULL
  AND (sqlc.narg('product_id')::text IS NULL OR product_id = sqlc.narg('product_id')::text);

-- name: ListVariantsByProductIDs :many
SELECT * FROM product_variant
WHERE product_id = ANY($1::text[]) AND deleted_at IS NULL
ORDER BY product_id, rank, id;

-- name: ListVariantsByIDs :many
SELECT * FROM product_variant
WHERE id = ANY($1::text[]) AND deleted_at IS NULL
ORDER BY product_id, rank, id;

-- name: UpdateVariant :one
UPDATE product_variant SET
    title            = COALESCE(sqlc.narg('title')::text, title),
    sku              = COALESCE(sqlc.narg('sku')::text, sku),
    barcode          = COALESCE(sqlc.narg('barcode')::text, barcode),
    ean              = COALESCE(sqlc.narg('ean')::text, ean),
    upc              = COALESCE(sqlc.narg('upc')::text, upc),
    manage_inventory = COALESCE(sqlc.narg('manage_inventory')::boolean, manage_inventory),
    allow_backorder  = COALESCE(sqlc.narg('allow_backorder')::boolean, allow_backorder),
    weight           = COALESCE(sqlc.narg('weight')::int, weight),
    rank             = COALESCE(sqlc.narg('rank')::int, rank),
    metadata         = COALESCE(sqlc.narg('metadata')::jsonb, metadata),
    updated_at       = now()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteVariant :execrows
UPDATE product_variant SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: CreateOption :one
INSERT INTO product_option (id, product_id, title, rank)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetOption :one
SELECT * FROM product_option
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListOptionsByProductIDs :many
SELECT * FROM product_option
WHERE product_id = ANY($1::text[]) AND deleted_at IS NULL
ORDER BY product_id, rank, id;

-- name: SoftDeleteOption :execrows
UPDATE product_option SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: CreateOptionValue :one
INSERT INTO product_option_value (id, option_id, value, rank)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListOptionValuesByOptionIDs :many
SELECT * FROM product_option_value
WHERE option_id = ANY($1::text[]) AND deleted_at IS NULL
ORDER BY option_id, rank, id;

-- name: ListOptionValuesByIDs :many
-- That the values to be attached to a variant really belong to options of THE
-- SAME PRODUCT is verified with the product_id this query returns.
SELECT ov.id, ov.option_id, ov.value, ov.rank, o.product_id, o.title AS option_title
FROM product_option_value ov
JOIN product_option o ON o.id = ov.option_id AND o.deleted_at IS NULL
WHERE ov.id = ANY($1::text[]) AND ov.deleted_at IS NULL
ORDER BY o.rank, ov.rank, ov.id;

-- CountVariantsUsingOptionValue counts the LIVE variants that carry the value.
--
-- It guards the delete below. The join to product_variant is the whole point:
-- product_variant_option_value has no deleted_at of its own, so a variant that
-- was soft-deleted long ago still has its rows in that table, and counting them
-- would refuse to delete a value that nothing living uses.
-- name: CountVariantsUsingOptionValue :one
SELECT count(*)
FROM product_variant_option_value vov
JOIN product_variant v ON v.id = vov.variant_id AND v.deleted_at IS NULL
WHERE vov.value_id = $1;

-- SoftDeleteOptionValue stamps one option value's deleted_at.
--
-- The write that was missing (docs/gaps.md D18). Its absence had a shape the
-- other three do not: an option value is on the PRODUCT PAGE, so a value added
-- with a typo — "Redd" next to "Red" — was on the storefront for good, and the
-- only escape was to delete the whole option and build it again, which takes
-- the variants' option assignments with it.
--
-- The FK from product_variant_option_value says ON DELETE CASCADE and cannot
-- fire against a soft delete, which is why the guard is not left to the
-- database: without it a value in use would vanish from
-- ListVariantOptionValuesByVariantIDs (that query joins the value with
-- "ov.deleted_at IS NULL") and the variant would silently show fewer options
-- than it has — two variants that differ only in that option would become
-- indistinguishable on the page.
-- name: SoftDeleteOptionValue :execrows
UPDATE product_option_value SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- SoftDeleteOptionValuesByOption deletes the values of ONE option.
--
-- Deleting an option already removes the whole option from every read (each one
-- joins product_option with "deleted_at IS NULL"), so this statement changes
-- nothing an API caller can see. It is here because the alternative is worse
-- than invisible: leaving live values under a dead option makes
-- product_option_value the one child table in this module whose rows outlive
-- their parent, and the next reader of the schema has to discover that by
-- experiment. No variant guard applies — the variants lost that option the
-- moment the option itself was stamped.
-- name: SoftDeleteOptionValuesByOption :execrows
UPDATE product_option_value SET deleted_at = now(), updated_at = now()
WHERE option_id = $1 AND deleted_at IS NULL;

-- SoftDeleteOptionValuesByProduct deletes the values of ALL of a product's
-- options.
--
-- The subquery does NOT filter the option's own deleted_at, deliberately: this
-- runs in the same transaction as SoftDeleteOptionsByProduct and must give the
-- same answer whichever of the two runs first.
-- name: SoftDeleteOptionValuesByProduct :execrows
UPDATE product_option_value SET deleted_at = now(), updated_at = now()
WHERE deleted_at IS NULL
  AND option_id IN (SELECT id FROM product_option WHERE product_id = $1);

-- name: SetVariantOptionValue :exec
-- Because the primary key is (variant_id, option_id), adding a second value to
-- the same option produces an UPDATE rather than a new row.
INSERT INTO product_variant_option_value (variant_id, option_id, value_id)
VALUES ($1, $2, $3)
ON CONFLICT (variant_id, option_id) DO UPDATE SET value_id = EXCLUDED.value_id;

-- name: DeleteVariantOptionValues :exec
DELETE FROM product_variant_option_value WHERE variant_id = $1;

-- name: ListVariantOptionValuesByVariantIDs :many
SELECT vov.variant_id, ov.id, ov.option_id, ov.value, ov.rank, o.title AS option_title
FROM product_variant_option_value vov
JOIN product_option_value ov ON ov.id = vov.value_id AND ov.deleted_at IS NULL
JOIN product_option o ON o.id = vov.option_id AND o.deleted_at IS NULL
WHERE vov.variant_id = ANY($1::text[])
ORDER BY vov.variant_id, o.rank, o.id;
