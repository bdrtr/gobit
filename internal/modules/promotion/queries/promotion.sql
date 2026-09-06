-- promotion sorguları.

-- name: InsertPromotion :one
INSERT INTO promotion (
    id, code, is_automatic, type, campaign_id, status,
    usage_limit, usage_count, metadata, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, 0, $8, $9, $9)
RETURNING *;

-- name: GetPromotion :one
SELECT * FROM promotion
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetPromotionByCode :one
SELECT * FROM promotion
WHERE code = $1 AND deleted_at IS NULL;

-- name: ListPromotions :many
SELECT * FROM promotion
WHERE deleted_at IS NULL
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (sqlc.narg('campaign_id')::text IS NULL OR campaign_id = sqlc.narg('campaign_id')::text)
ORDER BY id
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountPromotions sayfalama zarfının toplam sayısını verir ve ListPromotions
-- ile AYNI filtreleri uygular; ikisi birlikte değiştirilmelidir.
--
-- Toplam, satırlarla birlikte dönen bir pencere fonksiyonundan okunamaz:
-- aralık dışı bir sayfada hiç satır dönmez, pencere de değerlendirilmez ve
-- toplam 0 görünürdü.
-- name: CountPromotions :one
SELECT count(*) FROM promotion
WHERE deleted_at IS NULL
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (sqlc.narg('campaign_id')::text IS NULL OR campaign_id = sqlc.narg('campaign_id')::text);

-- GetPromotionsByIDs Query katmanının FetchByIDs çağrısını TEK turda karşılar.
-- name: GetPromotionsByIDs :many
SELECT * FROM promotion
WHERE id = ANY (@ids::text[]) AND deleted_at IS NULL
ORDER BY id;

-- ListApplicablePromotions hesaplamaya girebilecek promosyonları TEK turda
-- döner: aktif olanlardan OTOMATİK olanlar ve verilen KODLARA sahip olanlar.
--
-- Kod kümesi boş olabilir (yalnızca otomatikler); PostgreSQL'de boş bir dizi
-- ile ANY karşılaştırması hiçbir satır seçmez, bu yüzden ayrı bir dal
-- gerekmez.
--
-- Süzgecin SQL'de olması bilinçlidir: tüm promosyonları çekip uygulamada
-- elemek, promosyon sayısı büyüdükçe her sepet hesabında tüm tabloyu okumak
-- demek olurdu.
-- name: ListApplicablePromotions :many
SELECT * FROM promotion
WHERE deleted_at IS NULL
  AND status = 'active'
  AND (is_automatic OR code = ANY (@codes::text[]))
ORDER BY id;

-- UpdatePromotion promosyonun TANIMINI günceller.
--
-- usage_count BİLEREK dışarıdadır: sayacı yalnızca kullanım akışı değiştirir
-- (bkz. IncrementPromotionUsage / DecrementPromotionUsage).
-- name: UpdatePromotion :one
UPDATE promotion
SET code         = $2,
    is_automatic = $3,
    type         = $4,
    campaign_id  = $5,
    status       = $6,
    usage_limit  = $7,
    metadata     = $8,
    updated_at   = $9
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeletePromotion :one
UPDATE promotion
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- LockPromotion promosyonu işlem boyunca kilitler; kullanım akışının İLK
-- adımıdır.
--
-- Kilit sırası tektir ve her akışta aynıdır: ÖNCE promosyon, SONRA kampanya.
-- Sıranın ters dönmesi kilitlenme (deadlock) demektir; aynı kampanyaya bağlı
-- iki promosyon eşzamanlı kullanıldığında ikisi de aynı kampanya satırını
-- ister ve sıra ancak burada garanti edilir.
-- name: LockPromotion :one
SELECT * FROM promotion
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- LockPromotionShared promosyonu PAYLAŞIMLI kilitle okur; ALTINA satır yazan
-- yolların ilk adımıdır (kural ekleme, uygulama yöntemi yazma).
--
-- Kilit ŞARTTIR ve foreign key onun yerini TUTMAZ: promotion_rule ve
-- promotion_application_method promotion(id)'ye referans verir, ama silme
-- YUMUŞAKTIR ve satırı yerinde bırakır. FK denetimi satırın VARLIĞINA bakar,
-- deleted_at'ine değil; silinmiş bir promosyonun altına yazılan satırı bu
-- yüzden hiçbir kısıt durduramaz. Ölçüldü (2026-09-06): varlık denetimi ile
-- yazma arasına giren bir yumuşak silme, yazmayı beklet(me)den geçiriyordu.
--
-- FOR UPDATE değil FOR SHARE alınır: iki yönetici aynı promosyona aynı anda
-- kural ekleyebilmelidir ve iki FOR SHARE çakışmaz. Silme ise düz bir UPDATE'tir
-- ve satıra FOR NO KEY UPDATE kilidi koyar — FOR SHARE onunla ÇAKIŞIR, yani
-- yazma silmeyi bekler ve kilidi aldıktan sonra WHERE koşulunu YENİDEN
-- değerlendirip "kayıt yok" görür.
--
-- name: LockPromotionShared :one
SELECT * FROM promotion
WHERE id = $1 AND deleted_at IS NULL
FOR SHARE;

-- IncrementPromotionUsage kullanım sayacını KOŞULLU artırır.
--
-- Sınır aşılacaksa satır GÜNCELLENMEZ ve sorgu hiç satır dönmez; çağıran bunu
-- "kullanım hakkı bitti" olarak yorumlar (bkz. IncrementCampaignBudget'taki
-- aynı gerekçe).
-- name: IncrementPromotionUsage :one
UPDATE promotion
SET usage_count = usage_count + 1,
    updated_at  = @now::timestamptz
WHERE id = @id::text
  AND deleted_at IS NULL
  AND (usage_limit IS NULL OR usage_count + 1 <= usage_limit)
RETURNING *;

-- DecrementPromotionUsage kullanım sayacını düşürür ve SIFIRIN ALTINA İNMEZ.
-- Gerekçe DecrementCampaignBudget'takiyle aynıdır.
-- name: DecrementPromotionUsage :one
UPDATE promotion
SET usage_count = greatest(usage_count - 1, 0),
    updated_at  = @now::timestamptz
WHERE id = @id::text AND deleted_at IS NULL
RETURNING *;
