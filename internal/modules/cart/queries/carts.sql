-- carts sorguları.
--
-- Her okuma deleted_at IS NULL süzer (plan Bölüm 8: silme yumuşaktır).
-- Yazan sorgular ayrıca completed_at IS NULL şartı taşır: tamamlanmış sepet
-- DEĞİŞMEZDİR ve bu, servis kontrolünün yanında ikinci kapıdır.

-- name: CreateCart :one
INSERT INTO carts (
    id, region_id, customer_id, email, currency_code, metadata
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetCart :one
SELECT * FROM carts
WHERE id = $1 AND deleted_at IS NULL;

-- LockCart sepeti işlem boyunca kilitler ve güncel hâlini döner.
--
-- SEPETİ DEĞİŞTİREN HER AKIŞ BUNUNLA BAŞLAR. Kilit iki şeyi birden sağlar:
-- eşzamanlı iki AddLineItem sepetin satırlarını bozamaz (ikincisi birincinin
-- yazdığı satırı görür, yeni satır yerine adedi artırır) ve "tamamlanmış mı"
-- kontrolü ile yazma arasına başka bir işlem giremez.
-- name: LockCart :one
SELECT * FROM carts
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: ListCarts :many
SELECT * FROM carts
WHERE deleted_at IS NULL
  AND (sqlc.narg('customer_id')::text IS NULL OR customer_id = sqlc.narg('customer_id')::text)
  AND (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text)
  AND (sqlc.narg('completed')::boolean IS NULL
       OR (completed_at IS NOT NULL) = sqlc.narg('completed')::boolean)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountCarts sayfalama zarfının toplam sayısını verir ve ListCarts ile AYNI
-- filtreleri uygular; ikisi birlikte değiştirilmelidir.
--
-- Toplam, satırlarla birlikte dönen bir pencere fonksiyonundan okunamaz:
-- aralık dışı bir sayfada hiç satır dönmez, pencere değerlendirilmez ve toplam
-- 0 görünürdü. Toplam sayfanın değil FİLTRENİN sayısıdır.
-- name: CountCarts :one
SELECT COUNT(*) FROM carts
WHERE deleted_at IS NULL
  AND (sqlc.narg('customer_id')::text IS NULL OR customer_id = sqlc.narg('customer_id')::text)
  AND (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text)
  AND (sqlc.narg('completed')::boolean IS NULL
       OR (completed_at IS NOT NULL) = sqlc.narg('completed')::boolean);

-- GetCartsByIDs Query katmanının FetchByIDs çağrısını TEK turda karşılar;
-- kimlik başına sorgu (N+1) yapılmaz.
-- name: GetCartsByIDs :many
SELECT * FROM carts
WHERE id = ANY (sqlc.arg('ids')::text[]) AND deleted_at IS NULL
ORDER BY id;

-- UpdateCartContact sepetin e-postasını ve müşterisini MUTLAK değerle yazar.
--
-- Misafir sepetin kayıtlı müşteriye devri ve ödeme adımında toplanan e-posta
-- bu sorgudan geçer. Kimin kime devredilebileceği kararı SERVİSİNDİR (dolu bir
-- müşteriyi başkasıyla değiştirmek reddedilir); buradaki sorgu yalnızca yazar.
-- name: UpdateCartContact :one
UPDATE carts
SET email       = $2,
    customer_id = $3,
    updated_at  = now()
WHERE id = $1 AND deleted_at IS NULL AND completed_at IS NULL
RETURNING *;

-- UpdateCartTotals workflow'un hesapladığı toplamları yazar ve toplamların
-- hangi şekil için hesaplandığını damgalar.
-- name: UpdateCartTotals :one
UPDATE carts
SET subtotal        = $2,
    discount_total  = $3,
    tax_total       = $4,
    shipping_total  = $5,
    total           = $6,
    totals_revision = $7,
    updated_at      = now()
WHERE id = $1 AND deleted_at IS NULL AND completed_at IS NULL
RETURNING *;

-- BumpCartRevision sepetin şekil sayacını bir artırır; toplamları etkileyen
-- her yapısal değişiklikten sonra AYNI işlemde çağrılır.
-- name: BumpCartRevision :one
UPDATE carts
SET revision = revision + 1, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL AND completed_at IS NULL
RETURNING *;

-- MarkCartCompleted sepeti tamamlanmış olarak damgalar.
--
-- completed_at IS NULL şartı bilinçlidir: aynı sepeti ikinci kez tamamlamak
-- hiçbir satırı etkilemez ve çağıran bunu ayırt edebilir. Servis kilidi zaten
-- yarışı kapatır; buradaki şart doğrudan SQL ile yapılan bir müdahaleyi de
-- kapsayan ikinci kapıdır.
-- name: MarkCartCompleted :one
UPDATE carts
SET completed_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL AND completed_at IS NULL
RETURNING *;

-- name: SoftDeleteCart :execrows
UPDATE carts
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;
