-- inventory_items sorguları.

-- name: CreateInventoryItem :one
INSERT INTO inventory_items (
    id, sku, title, description, requires_shipping
) VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetInventoryItem :one
SELECT * FROM inventory_items
WHERE id = $1 AND deleted_at IS NULL;

-- LockInventoryItem kalemi bir işlem boyunca kilitler; (kalem, lokasyon)
-- seviyesini OLUŞTURAN akışlar bunu kullanır. Kilit, aynı kalem için eşzamanlı
-- iki oluşturmanın benzersiz indekse çarpmasını önler: satırı yaratacak olan
-- yarışı burada kazanır, diğeri bekler ve var olan satırı görür.
-- name: LockInventoryItem :one
SELECT id FROM inventory_items
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- LockInventoryItemShared kalemi PAYLAŞIMLI kilitler; seviye ve rezervasyon
-- satırlarına dokunan akışlar (Reserve/Release/Confirm/Adjust) bunu KİLİT
-- SIRASININ ilk adımı olarak kullanır.
--
-- Kilit sırası tektir ve her akışta aynıdır: önce kalem, sonra seviye. Sıranın
-- ters dönmesi kilitlenme (deadlock) demektir; rezervasyon satırının kaleme
-- verdiği foreign key zaten örtük bir FOR KEY SHARE kilidi ister, yani sıra
-- burada açıkça alınmazsa INSERT anında ters sırada alınırdı.
--
-- Kilit PAYLAŞIMLIDIR (FOR KEY SHARE): eşzamanlı iki rezervasyon birbirini
-- beklemez — onları zaten seviye satırının FOR UPDATE kilidi seri hâle
-- getirir. Kalemi yapısal olarak değiştiren akışlar (SetInventoryLevel,
-- DeleteInventoryItem) FOR UPDATE aldığı için bu kilitle ÇAKIŞIR ve sıra
-- korunur.
-- name: LockInventoryItemShared :one
SELECT id FROM inventory_items
WHERE id = $1 AND deleted_at IS NULL
FOR KEY SHARE;

-- name: ListInventoryItems :many
SELECT * FROM inventory_items
WHERE deleted_at IS NULL
  AND (sqlc.narg('sku')::text IS NULL OR sku = sqlc.narg('sku')::text)
  AND (sqlc.narg('requires_shipping')::boolean IS NULL
       OR requires_shipping = sqlc.narg('requires_shipping')::boolean)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountInventoryItems sayfalama zarfının toplam sayısını verir ve ListInventoryItems
-- ile AYNI filtreleri uygular; ikisi birlikte değiştirilmelidir.
--
-- Toplam, satırlarla birlikte dönen bir pencere fonksiyonundan (COUNT(*) OVER ())
-- okunamaz: aralık dışı bir sayfada hiç satır dönmez, pencere de değerlendirilmez
-- ve toplam 0 görünürdü. Toplam, sayfanın değil FİLTRENİN sayısıdır; bu yüzden
-- sayfalamadan bağımsız, ayrı bir sorgudur.
-- name: CountInventoryItems :one
SELECT COUNT(*) FROM inventory_items
WHERE deleted_at IS NULL
  AND (sqlc.narg('sku')::text IS NULL OR sku = sqlc.narg('sku')::text)
  AND (sqlc.narg('requires_shipping')::boolean IS NULL
       OR requires_shipping = sqlc.narg('requires_shipping')::boolean);

-- GetInventoryItemsByIDs Query katmanının FetchByIDs çağrısını TEK turda
-- karşılar; kimlik başına sorgu (N+1) yapılmaz.
-- name: GetInventoryItemsByIDs :many
SELECT * FROM inventory_items
WHERE id = ANY (sqlc.arg('ids')::text[]) AND deleted_at IS NULL
ORDER BY id;

-- name: SoftDeleteInventoryItem :execrows
UPDATE inventory_items
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;
