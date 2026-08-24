-- orders sorguları.
--
-- Her okuma deleted_at IS NULL süzer (plan Bölüm 8: silme yumuşaktır).
-- Durum değiştiren sorgular ayrıca BEKLENEN DURUMU şart koşar; bu, servisin
-- kilit altındaki kontrolünün yanında ikinci kapıdır ve doğrudan SQL ile
-- yapılan bir müdahaleyi de kapsar.

-- CreateOrder yeni bir sipariş yazar.
--
-- display_id sütunu BİLİNÇLİ OLARAK listede yoktur: GENERATED ALWAYS AS
-- IDENTITY sütununa açık değer yazılamaz ve numarayı sequence üretir.
-- Eşzamanlı iki INSERT'in aynı numarayı alması bu yüzden imkânsızdır.
-- name: CreateOrder :one
INSERT INTO orders (
    id, status, region_id, customer_id, email, currency_code,
    cart_id, idempotency_key,
    subtotal, discount_total, tax_total, shipping_total, total,
    metadata, placed_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8,
    $9, $10, $11, $12, $13,
    $14, now()
)
RETURNING *;

-- name: GetOrder :one
SELECT * FROM orders
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetOrderByDisplayID :one
SELECT * FROM orders
WHERE display_id = $1 AND deleted_at IS NULL;

-- GetOrderByIdempotencyKey aynı anahtarla açılmış siparişi döner.
--
-- Yeniden denenen bir saga adımı buradan mevcut siparişi bulur ve ikinci bir
-- sipariş açmaz (Prensip 2.6).
-- name: GetOrderByIdempotencyKey :one
SELECT * FROM orders
WHERE idempotency_key = $1 AND deleted_at IS NULL;

-- LockOrder siparişi işlem boyunca kilitler ve güncel hâlini döner.
--
-- DURUM DEĞİŞTİREN HER AKIŞ BUNUNLA BAŞLAR. Kilit, "durumu oku" ile "durumu
-- yaz" arasına başka bir işlemin girmesini engeller: aksi hâlde eşzamanlı bir
-- CancelOrder ve CompleteOrder ikisi de siparişi 'pending' görüp ikisi de
-- yazmaya kalkardı.
-- name: LockOrder :one
SELECT * FROM orders
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: ListOrders :many
SELECT * FROM orders
WHERE deleted_at IS NULL
  AND (sqlc.narg('customer_id')::text IS NULL OR customer_id = sqlc.narg('customer_id')::text)
  AND (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountOrders sayfalama zarfının toplam sayısını verir ve ListOrders ile AYNI
-- filtreleri uygular; ikisi birlikte değiştirilmelidir.
--
-- Toplam, satırlarla birlikte dönen bir pencere fonksiyonundan okunamaz:
-- aralık dışı bir sayfada hiç satır dönmez, pencere değerlendirilmez ve toplam
-- 0 görünürdü. Toplam sayfanın değil FİLTRENİN sayısıdır.
-- name: CountOrders :one
SELECT COUNT(*) FROM orders
WHERE deleted_at IS NULL
  AND (sqlc.narg('customer_id')::text IS NULL OR customer_id = sqlc.narg('customer_id')::text)
  AND (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text);

-- GetOrdersByIDs Query katmanının FetchByIDs çağrısını TEK turda karşılar;
-- kimlik başına sorgu (N+1) yapılmaz.
-- name: GetOrdersByIDs :many
SELECT * FROM orders
WHERE id = ANY (sqlc.arg('ids')::text[]) AND deleted_at IS NULL
ORDER BY id;

-- CancelOrder siparişi iptal eder ve iptal anını damgalar.
--
-- status = 'pending' şartı bilinçlidir: tamamlanmış ya da arşivlenmiş bir
-- sipariş buradan iptal EDİLEMEZ. Zaten iptal edilmiş bir siparişte de hiçbir
-- satır etkilenmez; çağıran bunu ayırt eder ve idempotent davranır
-- (bkz. service.Service.CancelOrder).
-- name: CancelOrder :one
UPDATE orders
SET status        = 'canceled',
    canceled_at   = now(),
    cancel_reason = $2,
    updated_at    = now()
WHERE id = $1 AND deleted_at IS NULL AND status = 'pending'
RETURNING *;

-- CompleteOrder siparişi tamamlanmış olarak damgalar.
-- name: CompleteOrder :one
UPDATE orders
SET status       = 'completed',
    completed_at = now(),
    updated_at   = now()
WHERE id = $1 AND deleted_at IS NULL AND status = 'pending'
RETURNING *;

-- ArchiveOrder tamamlanmış bir siparişi arşive alır.
--
-- completed_at'e DOKUNULMAZ: arşivleme siparişin tamamlanma anını değiştirmez,
-- yalnızca onu günlük listelerin dışına çıkarır.
-- name: ArchiveOrder :one
UPDATE orders
SET status     = 'archived',
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL AND status = 'completed'
RETURNING *;
