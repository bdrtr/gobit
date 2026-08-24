-- campaign sorguları.

-- name: InsertCampaign :one
INSERT INTO campaign (
    id, name, campaign_identifier, description, starts_at, ends_at,
    budget_type, budget_limit, budget_used, budget_currency_code,
    created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, $9, $10, $10)
RETURNING *;

-- name: GetCampaign :one
SELECT * FROM campaign
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetCampaignByIdentifier :one
SELECT * FROM campaign
WHERE campaign_identifier = $1 AND deleted_at IS NULL;

-- name: ListCampaigns :many
SELECT * FROM campaign
WHERE deleted_at IS NULL
ORDER BY id
LIMIT $1 OFFSET $2;

-- name: CountCampaigns :one
SELECT count(*) FROM campaign
WHERE deleted_at IS NULL;

-- GetCampaignsByIDs promosyon listelerinin kampanya üstverisini TEK turda
-- getirir; kampanya başına sorgu (N+1) yapılmaz.
-- name: GetCampaignsByIDs :many
SELECT * FROM campaign
WHERE id = ANY (@ids::text[]) AND deleted_at IS NULL
ORDER BY id;

-- UpdateCampaign kampanyanın TANIMINI günceller.
--
-- budget_used BİLEREK dışarıdadır: sayacı yalnızca kullanım akışı
-- (IncrementCampaignBudget / DecrementCampaignBudget) değiştirir. Yönetim
-- yüzeyinden yazılabilseydi, eşzamanlı bir kullanımla yarışıp sayacı geri
-- alabilirdi.
--
-- Sayaç sıfır DEĞİLKEN bütçenin BİRİMİ (türü ve para birimi) dondurulur;
-- WHERE'deki koşul budur. Sayaç bir birimde tutulur ve o birim değişse bile
-- SAYACIN KENDİSİ eski birimde kalırdı: "usage" (100 limit, 30 ADET
-- kullanılmış) bir kampanya "spend"e çevrildiğinde sayaçtaki 30 artık 30
-- KURUŞ sayılır; para birimi değişince de önceki TRY harcaması USD olarak
-- okunur ve devam eden TRY kullanımları campaign_budget_currency_mismatch ile
-- reddedilmeye başlar. İkisi de sessiz bir muhasebe bozulmasıdır.
--
-- Koşulun WHERE'de olması bilinçlidir: uygulamada "önce oku sonra yaz"
-- yapılsaydı, iki ifade arasına giren bir kullanım sayacı sıfırdan çıkarır ve
-- güncelleme yine de geçerdi. Sayaç sıfırlanmadan birim değiştirmek isteyen
-- operatör önce kullanımları serbest bırakmalıdır.
-- name: UpdateCampaign :one
UPDATE campaign
SET name                 = $2,
    campaign_identifier  = $3,
    description          = $4,
    starts_at            = $5,
    ends_at              = $6,
    budget_type          = $7,
    budget_limit         = $8,
    budget_currency_code = $9,
    updated_at           = $10
WHERE id = $1
  AND deleted_at IS NULL
  AND (
      budget_used = 0
      OR (budget_type = $7 AND budget_currency_code IS NOT DISTINCT FROM $9)
  )
RETURNING *;

-- name: SoftDeleteCampaign :one
UPDATE campaign
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- LockCampaign kampanyayı işlem boyunca kilitler.
--
-- EŞZAMANLI KULLANIMIN TEMELİ BUDUR. İki eşzamanlı Redeem aynı satırı
-- kilitlemek zorundadır; ikincisi birincinin işlemi bitene kadar bekler ve
-- READ COMMITTED altında satırın GÜNCEL bütçesini görür. "Önce oku sonra yaz"
-- yarışı bu yüzden oluşamaz: okuma zaten kilidin ardından yapılır.
--
-- Kilit sırası tektir ve her akışta aynıdır: ÖNCE promosyon, SONRA kampanya.
-- Sıranın ters dönmesi kilitlenme (deadlock) demektir.
-- name: LockCampaign :one
SELECT * FROM campaign
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- IncrementCampaignBudget bütçe sayacını KOŞULLU artırır.
--
-- Sınır aşılacaksa satır GÜNCELLENMEZ ve sorgu hiç satır dönmez; çağıran bunu
-- "bütçe yetmedi" olarak yorumlar. Koşulun WHERE'de olması bilinçlidir: sınır
-- kontrolünü uygulamada yapıp ayrı bir UPDATE atmak, kilit alınmamış bir yolda
-- iki ifade arasına başka bir kullanımın girmesine izin verirdi.
-- name: IncrementCampaignBudget :one
UPDATE campaign
SET budget_used = budget_used + @delta::bigint,
    updated_at  = @now::timestamptz
WHERE id = @id::text
  AND deleted_at IS NULL
  AND (budget_limit IS NULL OR budget_used + @delta::bigint <= budget_limit)
RETURNING *;

-- DecrementCampaignBudget bütçe sayacını düşürür ve SIFIRIN ALTINA İNMEZ.
--
-- greatest(...) bir savunmadır: defterle sayacın ayrıştığı bir durumda (elle
-- çalıştırılmış bir SQL, kısmi geri yükleme) geri alma negatif bir bütçe
-- yazmamalıdır. Negatif bütçe CHECK kısıtına çarpar ve serbest bırakmayı —
-- yani bir SAGA TELAFİSİNİ — düşürürdü.
-- name: DecrementCampaignBudget :one
UPDATE campaign
SET budget_used = greatest(budget_used - @delta::bigint, 0),
    updated_at  = @now::timestamptz
WHERE id = @id::text AND deleted_at IS NULL
RETURNING *;
