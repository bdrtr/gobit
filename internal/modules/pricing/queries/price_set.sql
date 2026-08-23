-- price_set sorguları. Tüm okumalar deleted_at IS NULL filtresi uygular.

-- name: InsertPriceSet :one
INSERT INTO price_set (id, created_at, updated_at)
VALUES ($1, $2, $2)
RETURNING *;

-- name: GetPriceSet :one
SELECT * FROM price_set
WHERE id = $1 AND deleted_at IS NULL;

-- GetPriceSetForUpdate kabı okur ve satırını İŞLEM SONUNA KADAR kilitler.
--
-- Kilitsiz bir varlık denetimi yerine koyma (replace) semantiğini korumaz: iki
-- eşzamanlı yazımdan ikincisinin "eski fiyatları sil" adımı READ COMMITTED
-- altında kendi statement snapshot'ında birincinin YENİ satırlarını göremez ve
-- onları silmez; sonuçta iki yazımın fiyatları kapta BİRLİKTE canlı kalır. Satır
-- kilidi aynı kaba yapılan yazımları seri hâle getirir.
--
-- FOR UPDATE kilit alındıktan sonra WHERE koşulunu YENİDEN değerlendirir; araya
-- giren bir silme bu yüzden "kayıt yok" olarak görünür ve fiyatlar silinmiş bir
-- kaba yapışmaz.
-- name: GetPriceSetForUpdate :one
SELECT * FROM price_set
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: ListPriceSets :many
SELECT * FROM price_set
WHERE deleted_at IS NULL
ORDER BY id
LIMIT $1 OFFSET $2;

-- name: CountPriceSets :one
SELECT count(*) FROM price_set
WHERE deleted_at IS NULL;

-- name: GetPriceSetsByIDs :many
SELECT * FROM price_set
WHERE id = ANY(@ids::text[]) AND deleted_at IS NULL
ORDER BY id;

-- name: SoftDeletePriceSet :one
UPDATE price_set
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;
