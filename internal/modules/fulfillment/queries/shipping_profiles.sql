-- shipping_profiles sorguları.
--
-- Profil, kargo seçeneklerinin kabıdır ve hangi ürünlere bağlı olduğunu
-- BİLMEZ: ürün-profil bağı Module Links üzerinden kurulur (Prensip 2.1/2.2).

-- name: CreateShippingProfile :one
INSERT INTO shipping_profiles (id, name, type, metadata)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetShippingProfile :one
SELECT * FROM shipping_profiles
WHERE id = $1 AND deleted_at IS NULL;

-- LockShippingProfile profili İŞLEM SONUNA KADAR yazma kilidiyle okur.
--
-- Yumuşak silme, anahtar OLMAYAN bir sütunu güncellediği için kendiliğinden
-- yalnızca FOR NO KEY UPDATE alır; o kilit, bir seçenek INSERT'ünün foreign key
-- için aldığı FOR KEY SHARE ile ÇAKIŞMAZ. Yani "seçeneği var mı" kontrolüyle
-- silme arasına giren bir CreateShippingOption beklemeden tamamlanır ve geriye
-- silinmiş bir profile bağlı CANLI bir seçenek kalırdı (READ COMMITTED'de tek
-- işlem bunu tek başına engellemez). FOR UPDATE, o çakışmayı kuran kilittir.
-- name: LockShippingProfile :one
SELECT * FROM shipping_profiles
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- LockShippingProfileShared profili İŞLEM SONUNA KADAR paylaşımlı kilitle
-- okur.
--
-- Seçenek oluşturma yolu bunu kullanır: birbirine paralel seçenek eklemeleri
-- BEKLEMEZ (FOR SHARE kendisiyle çakışmaz), ama profili silmeye çalışan
-- LockShippingProfile ile çakışır. İki yol böylece serileşir.
-- name: LockShippingProfileShared :one
SELECT * FROM shipping_profiles
WHERE id = $1 AND deleted_at IS NULL
FOR SHARE;

-- name: ListShippingProfiles :many
SELECT * FROM shipping_profiles
WHERE deleted_at IS NULL
  AND (sqlc.narg('type')::text IS NULL OR type = sqlc.narg('type')::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountShippingProfiles sayfalama zarfının toplam sayısını verir ve
-- ListShippingProfiles ile AYNI filtreleri uygular; ikisi birlikte
-- değiştirilmelidir.
--
-- Toplam, satırlarla birlikte dönen bir pencere fonksiyonundan okunamaz:
-- aralık dışı bir sayfada hiç satır dönmez, pencere değerlendirilmez ve toplam
-- 0 görünürdü. Toplam sayfanın değil FİLTRENİN sayısıdır.
-- name: CountShippingProfiles :one
SELECT COUNT(*) FROM shipping_profiles
WHERE deleted_at IS NULL
  AND (sqlc.narg('type')::text IS NULL OR type = sqlc.narg('type')::text);

-- name: UpdateShippingProfile :one
UPDATE shipping_profiles
SET name       = $2,
    type       = $3,
    metadata   = $4,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- SoftDeleteShippingProfile profili YUMUŞAK siler (plan Bölüm 8).
--
-- Satır fiziksel olarak kalır: gönderisi olan bir seçeneğin profili
-- silindiğinde geçmiş kayıtların kime ait olduğu okunabilmelidir.
-- name: SoftDeleteShippingProfile :one
UPDATE shipping_profiles
SET deleted_at = now(),
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- CountAliveOptionsByProfile profile bağlı YAŞAYAN seçenekleri sayar.
--
-- Silme öncesi kontrol içindir: seçeneği duran bir profili silmek, ürünlerin
-- kargo kuralını sessizce ortadan kaldırırdı.
-- name: CountAliveOptionsByProfile :one
SELECT COUNT(*) FROM shipping_options
WHERE shipping_profile_id = $1 AND deleted_at IS NULL;
