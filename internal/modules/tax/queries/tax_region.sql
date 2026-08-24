-- tax_region sorguları. Tüm okumalar deleted_at IS NULL süzer.

-- name: InsertTaxRegion :one
INSERT INTO tax_region (id, country_code, province_code, parent_id, provider_id, metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
RETURNING *;

-- name: GetTaxRegion :one
SELECT * FROM tax_region
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetTaxRegionsByIDs :many
SELECT * FROM tax_region
WHERE id = ANY(@ids::text[]) AND deleted_at IS NULL
ORDER BY id;

-- name: ListTaxRegions :many
SELECT * FROM tax_region
WHERE deleted_at IS NULL
  AND (@country_code::text = '' OR country_code = @country_code::text)
ORDER BY country_code, (parent_id IS NULL) DESC, id
LIMIT $1 OFFSET $2;

-- name: CountTaxRegions :one
SELECT count(*) FROM tax_region
WHERE deleted_at IS NULL
  AND (@country_code::text = '' OR country_code = @country_code::text);

-- ResolveTaxRegions bir ülkenin kökünü ve (verilmişse) eyalet bölgesini TEK
-- sorguda döner.
--
-- Tek sorgu olması bilinçlidir: hesap yolu her sepet turunda çağrılır ve iki
-- gidiş dönüş, bölge çözümünün maliyetini iki katına çıkarırdı. Eyalet kodu
-- boş verildiğinde ikinci koşul hiçbir satırla eşleşmez (province_code CHECK
-- gereği boş olamaz), yani yalnızca kök döner.
--
-- Sıra EYALET ÖNCE'dir: hesap zinciri en ÖZELDEN genele yürür ve sıranın
-- sorguda sabitlenmesi, servisin satırları yeniden sıralamak zorunda
-- kalmamasını sağlar.
-- name: ResolveTaxRegions :many
SELECT * FROM tax_region
WHERE deleted_at IS NULL
  AND country_code = @country_code::text
  AND (parent_id IS NULL OR province_code = @province_code::text)
ORDER BY (parent_id IS NULL), id;

-- name: GetTaxRegionForUpdate :one
SELECT * FROM tax_region
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- SoftDeleteTaxRegionTree bölgeyi ve (kök ise) alt bölgelerini birlikte siler.
--
-- Ağaç iki seviye olduğu için "kendisi ya da çocuğu" koşulu tüm alt ağacı
-- kapsar; özyinelemeli bir CTE gerekmez. Silinen kimlikler DÖNER: çağıran,
-- oranları aynı işlem içinde bu kimliklerle siler. Bölge silinip oranları
-- kalsaydı, aynı ülkeye açılan yeni bir bölge eski oranları görmez ama eski
-- oranlar yetim satır olarak defterde durur ve rapor toplamlarını bozardı.
-- name: SoftDeleteTaxRegionTree :many
UPDATE tax_region
SET deleted_at = @deleted_at, updated_at = @deleted_at
WHERE deleted_at IS NULL AND (id = @id::text OR parent_id = @id::text)
RETURNING id;
