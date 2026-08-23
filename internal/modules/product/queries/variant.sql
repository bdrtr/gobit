-- Varyant ve seçenek sorguları.
--
-- Toplu (ByIDs) sorgular tesadüf değildir: hem Query sağlayıcısının
-- FetchByIDs'i hem de ürün listelemesinin varyant/seçenek doldurması TEK
-- sorguyla çalışmak zorundadır; kayıt başına sorgu N+1 üretirdi.

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
-- Varyanta bağlanacak değerlerin gerçekten AYNI ÜRÜNÜN seçeneklerine ait
-- olduğu bu sorgunun döndürdüğü product_id ile doğrulanır.
SELECT ov.id, ov.option_id, ov.value, ov.rank, o.product_id, o.title AS option_title
FROM product_option_value ov
JOIN product_option o ON o.id = ov.option_id AND o.deleted_at IS NULL
WHERE ov.id = ANY($1::text[]) AND ov.deleted_at IS NULL
ORDER BY o.rank, ov.rank, ov.id;

-- name: SetVariantOptionValue :exec
-- Birincil anahtar (variant_id, option_id) olduğu için aynı seçeneğe ikinci bir
-- değer eklemek yeni satır değil, GÜNCELLEME üretir.
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
