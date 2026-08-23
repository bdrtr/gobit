-- region sorguları. Tüm okumalar deleted_at IS NULL filtresi uygular.

-- name: InsertRegion :one
INSERT INTO region (id, name, currency_code, automatic_taxes, tax_rate, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $6)
RETURNING *;

-- name: GetRegion :one
SELECT * FROM region
WHERE id = $1 AND deleted_at IS NULL;

-- GetRegionForUpdate bölgeyi okur ve satırını İŞLEM SONUNA KADAR kilitler.
--
-- Kısmi güncelleme (yama) ve silme bu kilit altında yapılır: yama, okunan
-- satırın üstüne yazıldığı için kilitsiz iki eşzamanlı güncelleme birbirinin
-- alanını geri alabilirdi (lost update). FOR UPDATE, kilit alındıktan sonra
-- WHERE koşulunu YENİDEN değerlendirir; araya giren bir silme bu yüzden
-- "kayıt yok" olarak görünür.
-- name: GetRegionForUpdate :one
SELECT * FROM region
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- GetRegionForShare bölgeyi okur ve PAYLAŞIMLI kilitler.
--
-- Ülke atayan akışın KİLİT SIRASINDAKİ ilk adımıdır: önce bölge, sonra ülke.
-- Sıra her akışta aynıdır; ters dönmesi kilitlenme (deadlock) demektir.
--
-- Kilit paylaşımlıdır: farklı ülkeleri aynı bölgeye ekleyen iki istek
-- birbirini beklemez. Yine de bölgeyi DEĞİŞTİREN akışlarla (silme, güncelleme)
-- çakışır, yani silinmekte olan bir bölgeye ülke eklenemez.
-- name: GetRegionForShare :one
SELECT * FROM region
WHERE id = $1 AND deleted_at IS NULL
FOR SHARE;

-- name: ListRegions :many
SELECT * FROM region
WHERE deleted_at IS NULL
ORDER BY id
LIMIT $1 OFFSET $2;

-- name: CountRegions :one
SELECT count(*) FROM region
WHERE deleted_at IS NULL;

-- name: GetRegionsByIDs :many
SELECT * FROM region
WHERE id = ANY(@ids::text[]) AND deleted_at IS NULL
ORDER BY id;

-- name: UpdateRegion :one
UPDATE region
SET name = $2, currency_code = $3, automatic_taxes = $4, tax_rate = $5, updated_at = $6
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteRegion :one
UPDATE region
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- GetRegionByCountry ülkeden bölgeye TEK turda gider.
--
-- Sepet oluşturulurken kullanılan yoldur (ResolveRegionForCountry). Ülkenin
-- kendisi de bölgesi de bulunamadığında ayrım yapılmaz; hangi durumun
-- geçerli olduğunu servis, YALNIZCA hata yolunda ikinci bir sorguyla ayırır.
-- Böylece mutlu yol tek sorgu kalır.
-- name: GetRegionByCountry :one
SELECT r.* FROM country c
JOIN region r ON r.id = c.region_id AND r.deleted_at IS NULL
WHERE c.iso_2 = $1 AND c.deleted_at IS NULL;
