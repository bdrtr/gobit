-- country sorguları. Tüm okumalar deleted_at IS NULL filtresi uygular.

-- name: GetCountry :one
SELECT * FROM country
WHERE iso_2 = $1 AND deleted_at IS NULL;

-- GetCountryForUpdate ülkeyi okur ve satırını İŞLEM SONUNA KADAR kilitler.
--
-- "Bir ülke en fazla bir bölgeye ait olabilir" kuralının eşzamanlılık
-- ayağıdır. Kilitsiz bir "önce oku, sonra yaz" akışında aynı ülkeyi iki farklı
-- bölgeye ekleyen iki istek de region_id'yi boş görür ve ikincisi birincinin
-- yazdığını sessizce ezerdi. Kilit ikincisini bekletir; beklemesi bitince
-- satırın GÜNCEL sürümünü okur ve çakışmayı görür.
--
-- Kilit sırasının ikinci adımıdır: önce bölge (GetRegionForShare), sonra ülke.
-- name: GetCountryForUpdate :one
SELECT * FROM country
WHERE iso_2 = $1 AND deleted_at IS NULL
FOR UPDATE;

-- ListCountries ülkeleri sayfalayarak döner; bölge süzgeci isteğe bağlıdır.
--
-- NULL region_id "süzme" demektir, belirli bir bölge kimliği ise o bölgenin
-- ülkeleri demektir. "Hiçbir bölgeye bağlı olmayan ülkeler" ayrı bir istek
-- olurdu ve bilinçli olarak sunulmaz: yönetim yüzeyi için tüm liste yeterlidir.
-- name: ListCountries :many
SELECT * FROM country
WHERE deleted_at IS NULL
  AND (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text)
ORDER BY iso_2
LIMIT @lim::integer OFFSET @off::integer;

-- name: CountCountries :one
SELECT count(*) FROM country
WHERE deleted_at IS NULL
  AND (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text);

-- ListCountriesByRegions birden çok bölgenin ülkelerini TEK turda okur.
--
-- Query sağlayıcısı bölgeleri ülkeleriyle döndürür; bölge başına ayrı sorgu
-- N+1 demek olurdu (ADR 0004'ün toplu okuma şartı).
-- name: ListCountriesByRegions :many
SELECT * FROM country
WHERE region_id = ANY(@region_ids::text[]) AND deleted_at IS NULL
ORDER BY region_id, iso_2;

-- name: SetCountryRegion :one
UPDATE country
SET region_id = @region_id::text, updated_at = @updated_at::timestamptz
WHERE iso_2 = @iso_2::text AND deleted_at IS NULL
RETURNING *;

-- ClearCountryRegion ülkeyi bölgesinden ayırır.
--
-- Koşula bölge kimliği de girer: başka bir bölgenin ülkesini yanlışlıkla
-- serbest bırakan bir istek satır bulamaz ve hata alır.
-- name: ClearCountryRegion :one
UPDATE country
SET region_id = NULL, updated_at = @updated_at::timestamptz
WHERE iso_2 = @iso_2::text AND region_id = @region_id::text AND deleted_at IS NULL
RETURNING *;

-- ClearRegionCountries bir bölgenin TÜM ülkelerini serbest bırakır.
--
-- Bölge silinirken çağrılır. Çağrılmasaydı ülkeler ölü bir bölgeye bağlı
-- kalır, başka bir bölgeye eklenemez ve ResolveRegionForCountry o ülkeler için
-- kalıcı olarak "bulunamadı" dönerdi.
-- name: ClearRegionCountries :exec
UPDATE country
SET region_id = NULL, updated_at = @updated_at::timestamptz
WHERE region_id = @region_id::text AND deleted_at IS NULL;
