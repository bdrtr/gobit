-- Ürün sorguları.
--
-- Ortak kurallar:
--   * Her okuma "deleted_at IS NULL" filtresi uygular (plan Bölüm 8: soft delete).
--   * Listeleme sırası (created_at DESC, id DESC) sabittir; ikinci anahtar,
--     aynı milisaniyede oluşmuş iki kaydın sayfalar arasında yer değiştirmesini
--     engeller.
--   * Zaman damgalarını veritabanı üretir (now()); tek saat kaynağı budur.

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
-- GetProductForUpdate ürünü SATIR KİLİDİYLE okur; yalnızca bir işlem içinde
-- anlamlıdır.
--
-- Varyant yazmadan önce sahibin var olduğunu doğrulamak tek başına yetmez:
-- silme SOFT olduğu için foreign key boşluğu kapatmaz ve eşzamanlı bir silme
-- kontrol ile INSERT arasına girerse sahibi silinmiş bir varyant ortaya çıkar.
-- Kilit, silmeyi bu işlemle SIRAYA DİZER: silme önce gelirse burada satır
-- bulunamaz, sonra gelirse varyant silmenin temizliğine yetişir.
SELECT * FROM product
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: GetProductByHandle :one
SELECT * FROM product
WHERE handle = $1 AND deleted_at IS NULL;

-- name: ListProducts :many
SELECT * FROM product
WHERE deleted_at IS NULL
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (sqlc.narg('collection_id')::text IS NULL OR collection_id = sqlc.narg('collection_id')::text)
  AND (sqlc.narg('handle')::text IS NULL OR handle = sqlc.narg('handle')::text)
  AND (sqlc.narg('search')::text IS NULL OR title ILIKE '%' || sqlc.narg('search')::text || '%')
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountProducts :one
SELECT count(*) FROM product
WHERE deleted_at IS NULL
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (sqlc.narg('collection_id')::text IS NULL OR collection_id = sqlc.narg('collection_id')::text)
  AND (sqlc.narg('handle')::text IS NULL OR handle = sqlc.narg('handle')::text)
  AND (sqlc.narg('search')::text IS NULL OR title ILIKE '%' || sqlc.narg('search')::text || '%');

-- name: ListProductsByIDs :many
SELECT * FROM product
WHERE id = ANY($1::text[]) AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC;

-- name: UpdateProduct :one
-- COALESCE kalıbı: NULL geçilen alan DEĞİŞMEZ. Bunun bilinen sınırı, bir alanı
-- NULL'a çekmenin (örn. subtitle'ı silmenin) bu uçtan yapılamamasıdır; PATCH
-- sözleşmesi "verilmeyen alan korunur" olarak belgelenmiştir.
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
-- Ürün silinirken varyantların link'leri (fiyat/stok) temizlenir; bunun için
-- silinmeden ÖNCE kimlikler okunur.
SELECT id FROM product_variant
WHERE product_id = $1 AND deleted_at IS NULL
ORDER BY rank, id;

-- name: CreateImage :one
INSERT INTO product_image (id, product_id, url, rank, metadata)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListImagesByProductIDs :many
SELECT * FROM product_image
WHERE product_id = ANY($1::text[]) AND deleted_at IS NULL
ORDER BY product_id, rank, id;

-- name: DeleteImagesByProduct :exec
UPDATE product_image SET deleted_at = now(), updated_at = now()
WHERE product_id = $1 AND deleted_at IS NULL;
