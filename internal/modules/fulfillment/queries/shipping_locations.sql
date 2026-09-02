-- shipping_locations ve shipping_location_regions sorguları.
--
-- Okuma yolu ikiye ayrılır ve ayrım shipping_options'ınkiyle AYNI gerekçeye
-- dayanır:
--
--   - Yönetim listelemesi (ListShippingLocations) — sayfalanır.
--   - Seçim okuması (ShippingLocationPolicies) — aday depoların kararı
--     etkileyen OLGULARINI tek turda döner. Politikanın KENDİSİ burada
--     çalışmaz; eleme ve sıralama servis katmanındaki saf fonksiyonda yaşar ve
--     veritabanı olmadan birim testiyle kanıtlanabilir.
--
-- Silme YUMUŞAK DEĞİLDİR; gerekçesi migration'ın başındadır. Bu yüzden
-- sorguların hiçbirinde deleted_at süzgeci yoktur ve olmaması bir unutma
-- değildir.

-- UpsertShippingLocation politikayı YAZAR ya da ÜZERİNE yazar.
--
-- Ayrı bir Create/Update çifti yerine tek upsert olmasının sebebi, yüzeyin
-- ifade ettiği şeyin bir VARLIK oluşturmak değil bir depoya ait AYARI
-- belirlemek olmasıdır: çağıran "bu depo şu öncelikte" der ve satırın daha önce
-- var olup olmadığı onun sorunu değildir.
-- name: UpsertShippingLocation :one
INSERT INTO shipping_locations (location_id, priority)
VALUES ($1, $2)
ON CONFLICT (location_id) DO UPDATE
    SET priority = EXCLUDED.priority, updated_at = now()
RETURNING *;

-- GetShippingLocation politikayı BÖLGELERİYLE BİRLİKTE tek deyimde döner.
--
-- İki ayrı SELECT ile okumak (önce satır, sonra bağlar) yırtık bir kayıt
-- üretirdi: işlem dışında yapılan iki okuma iki ayrı anlık görüntüden gelir ve
-- aralarına giren bir yazma, deponun YENİ önceliğiyle ESKİ bölgelerini yan yana
-- gösterirdi. Yazma yolu bunu işlemle kapatıyor; okuma yolu tek deyimle
-- kapatır. Kalıp ShippingLocationPolicies'in aynısıdır.
-- name: GetShippingLocation :one
SELECT
    l.location_id,
    l.priority,
    l.created_at,
    l.updated_at,
    COALESCE(
        array_agg(r.region_id ORDER BY r.region_id) FILTER (WHERE r.region_id IS NOT NULL),
        '{}'
    )::text[] AS region_ids
FROM shipping_locations l
LEFT JOIN shipping_location_regions r ON r.location_id = l.location_id
WHERE l.location_id = $1
GROUP BY l.location_id, l.priority, l.created_at, l.updated_at;

-- ListShippingLocations politikaları bağlarıyla birlikte sayfalar.
--
-- Bağlar burada da AYNI deyimde toplanır; sayfadaki depo başına ikinci bir
-- sorgu (N+1) yapılmaz ve tekil okumayla aynı yırtılma kapısı kapalı kalır.
-- name: ListShippingLocations :many
SELECT
    l.location_id,
    l.priority,
    l.created_at,
    l.updated_at,
    COALESCE(
        array_agg(r.region_id ORDER BY r.region_id) FILTER (WHERE r.region_id IS NOT NULL),
        '{}'
    )::text[] AS region_ids
FROM shipping_locations l
LEFT JOIN shipping_location_regions r ON r.location_id = l.location_id
GROUP BY l.location_id, l.priority, l.created_at, l.updated_at
ORDER BY l.priority, l.location_id
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountShippingLocations ListShippingLocations ile AYNI kümeyi sayar; ikisi
-- birlikte değişmek zorundadır.
-- name: CountShippingLocations :one
SELECT COUNT(*) FROM shipping_locations;

-- DeleteShippingLocation politikayı KALICI olarak siler; bölge bağları
-- ON DELETE CASCADE ile birlikte düşer.
--
-- Silmenin anlamı "depoyu kapat" DEĞİL, "depoyu varsayılana döndür"dür:
-- politikası olmayan depo varsayılan öncelikte ve tüm bölgelere hizmet ediyor
-- sayılır.
-- name: DeleteShippingLocation :execrows
DELETE FROM shipping_locations
WHERE location_id = $1;

-- ShippingLocationPolicies aday depoların kararı etkileyen olgularını TEK
-- turda döner; aday başına sorgu (N+1) yapılmaz.
--
-- Dönen satır YALNIZCA politikası olan depolar içindir. Listede olmayan aday
-- "politikasız"dır ve varsayılan sayılır (öncelik 0, tüm bölgelere hizmet
-- eder); bu ayrımı çağıran yapar, sorgu değil.
--
-- Bölge bağları SAYI ya da BAYRAK olarak değil, KİMLİK DİZİSİ olarak döner ve
-- bu bilinçlidir. İki sebebi var: kural ("bağı olmayan tüm bölgelere hizmet
-- eder, olan yalnızca bağlılarına") saf bir fonksiyonda, veritabanı olmadan
-- sınanabilir kalır; ve tüm adaylar elendiğinde hata mesajı depoların GERÇEKTE
-- hangi bölgelere bağlı olduğunu yazabilir. İkincisi bir konfor değil,
-- teşhisin tek yoludur: silinip yeniden açılmış bir bölgenin kimliği hiçbir
-- yerde eşleşmez ve bayrak dönen bir sorguyla operatör yalnızca "hizmet eden
-- depo yok" görürdü.
--
-- FILTER, bağı olmayan depo için '{}' üretir; onsuz LEFT JOIN tek elemanı NULL
-- olan bir dizi döndürür ve "bağı yok" ile "bağı NULL" ayırt edilemezdi.
-- name: ShippingLocationPolicies :many
SELECT
    l.location_id,
    l.priority,
    COALESCE(
        array_agg(r.region_id ORDER BY r.region_id) FILTER (WHERE r.region_id IS NOT NULL),
        '{}'
    )::text[] AS region_ids
FROM shipping_locations l
LEFT JOIN shipping_location_regions r ON r.location_id = l.location_id
WHERE l.location_id = ANY (sqlc.arg('location_ids')::text[])
GROUP BY l.location_id, l.priority
ORDER BY l.location_id;

-- ReplaceShippingLocationRegions bir deponun bölge bağlarını TOPTAN yazar:
-- önce hepsi silinir, sonra verilenler yazılır. Çağıran ikisini AYNI işlemde
-- çağırmalıdır, yoksa arada kalan bir okuma depoyu bölgesiz (yani tüm
-- bölgelere açık) görürdü.
-- name: DeleteShippingLocationRegions :exec
DELETE FROM shipping_location_regions
WHERE location_id = $1;

-- InsertShippingLocationRegions bölge bağlarını TEK deyimde yazar; bölge başına
-- INSERT yapılmaz. Yinelenen bir bölge kimliği sessizce yutulur (ON CONFLICT
-- DO NOTHING) çünkü "aynı bölgeyi iki kez bağlamak" ifade edilmek istenen şeyle
-- aynı sonucu verir ve çağıranı bir çakışma hatasıyla karşılamak yanıltıcı
-- olurdu.
-- name: InsertShippingLocationRegions :exec
INSERT INTO shipping_location_regions (location_id, region_id)
SELECT sqlc.arg('location_id')::text, region
FROM unnest(sqlc.arg('region_ids')::text[]) AS region
ON CONFLICT (location_id, region_id) DO NOTHING;

