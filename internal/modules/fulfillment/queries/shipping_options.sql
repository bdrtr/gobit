-- shipping_options sorguları.
--
-- Seçenek kataloğunun okuma yolu ikiye ayrılır:
--
--   - Yönetim listelemesi (ListShippingOptions) — sayfalanır ve süzülür.
--   - Uygunluk listelemesi (ListEligibleShippingOptions) — bir sepet bağlamı
--     için ADAYLARI döner. Kural eşleşmesi burada YAPILMAZ; yalnızca sütun
--     düzeyinde ucuz olan elemeler (bölge, para birimi, profil, iade,
--     admin_only) SQL'e verilir. Kuralın kendisi servis katmanındaki saf
--     fonksiyonda yaşar ve veritabanı olmadan birim testiyle kanıtlanabilir.

-- name: CreateShippingOption :one
INSERT INTO shipping_options (
    id, name, provider_id, shipping_profile_id, price_type, amount,
    currency_code, region_id, is_return, admin_only, data, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: GetShippingOption :one
SELECT * FROM shipping_options
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListShippingOptions :many
SELECT * FROM shipping_options
WHERE deleted_at IS NULL
  AND (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text)
  AND (sqlc.narg('profile_id')::text IS NULL OR shipping_profile_id = sqlc.narg('profile_id')::text)
  AND (sqlc.narg('provider_id')::text IS NULL OR provider_id = sqlc.narg('provider_id')::text)
  AND (sqlc.narg('price_type')::text IS NULL OR price_type = sqlc.narg('price_type')::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountShippingOptions ListShippingOptions ile AYNI filtreleri uygular;
-- gerekçe için bkz. CountShippingProfiles.
-- name: CountShippingOptions :one
SELECT COUNT(*) FROM shipping_options
WHERE deleted_at IS NULL
  AND (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text)
  AND (sqlc.narg('profile_id')::text IS NULL OR shipping_profile_id = sqlc.narg('profile_id')::text)
  AND (sqlc.narg('provider_id')::text IS NULL OR provider_id = sqlc.narg('provider_id')::text)
  AND (sqlc.narg('price_type')::text IS NULL OR price_type = sqlc.narg('price_type')::text);

-- GetShippingOptionsByIDs Query katmanının FetchByIDs çağrısını TEK turda
-- karşılar; kimlik başına sorgu (N+1) yapılmaz.
-- name: GetShippingOptionsByIDs :many
SELECT * FROM shipping_options
WHERE id = ANY (sqlc.arg('ids')::text[]) AND deleted_at IS NULL
ORDER BY id;

-- ListEligibleShippingOptions bir sepet bağlamının ADAYLARINI döner.
--
-- region_id filtresi İKİ değeri kabul eder: seçeneğin bölgesi ya istenen bölge
-- ya da BOŞ dizedir. Boş, "her bölge" demektir ve bölgesi olmayan bir mağazanın
-- (ya da bölgeden bağımsız bir seçeneğin) listeden düşmesini engeller.
--
-- profile_ids boş dizi verilirse profil süzgeci UYGULANMAZ: sepetin ürünleri
-- hiçbir profile bağlı değilse tüm profiller aday olur. Aksi hâlde boş bir
-- sepet hiçbir kargo seçeneği göremezdi.
--
-- include_admin_only false ise admin_only seçenekler ELENİR. Süzgeç SQL'de
-- durur, çünkü mağaza yüzeyine sızmaması gereken tek alan budur ve satırın hiç
-- okunmaması, okunup sonra atılmasından daha güvenlidir.
--
-- PROFİLİ SİLİNMİŞ seçenek de elenir. Normal akışta böyle bir satır oluşamaz
-- (profil silme, bağlı seçenek varsa reddedilir ve profil satırı o sırada
-- kilitlidir), ama doğrudan SQL çalıştıran bir bakım betiği ya da kısmi bir
-- geri yükleme onu üretebilir. Uygunluk sorgusu, okuduğu her satıra dayanıklı
-- olmalıdır: kargo kuralı ortadan kalkmış bir profilin seçeneği vitrinde
-- durmamalıdır.
-- name: ListEligibleShippingOptions :many
SELECT shipping_options.* FROM shipping_options
JOIN shipping_profiles
  ON shipping_profiles.id = shipping_options.shipping_profile_id
 AND shipping_profiles.deleted_at IS NULL
WHERE shipping_options.deleted_at IS NULL
  AND (region_id = sqlc.arg('region_id')::text OR region_id = '')
  AND currency_code = sqlc.arg('currency_code')::text
  AND is_return = sqlc.arg('is_return')::boolean
  AND (sqlc.arg('include_admin_only')::boolean OR admin_only = FALSE)
  AND (cardinality(sqlc.arg('profile_ids')::text[]) = 0
       OR shipping_profile_id = ANY (sqlc.arg('profile_ids')::text[]))
ORDER BY shipping_options.id;

-- name: UpdateShippingOption :one
UPDATE shipping_options
SET name       = $2,
    price_type = $3,
    amount     = $4,
    region_id  = $5,
    is_return  = $6,
    admin_only = $7,
    data       = $8,
    metadata   = $9,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- SoftDeleteShippingOption seçeneği YUMUŞAK siler (plan Bölüm 8).
--
-- Fiziksel silme, seçeneğe bağlı gönderilerin (fulfillments.shipping_option_id)
-- ON DELETE RESTRICT kısıtına takılırdı; yumuşak silme geçmişi bozmadan
-- seçeneği kataloğun dışına çıkarır.
-- name: SoftDeleteShippingOption :one
UPDATE shipping_options
SET deleted_at = now(),
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;
