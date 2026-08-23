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

-- name: ListInventoryItems :many
SELECT *, COUNT(*) OVER () AS total_count
FROM inventory_items
WHERE deleted_at IS NULL
  AND (sqlc.narg('sku')::text IS NULL OR sku = sqlc.narg('sku')::text)
  AND (sqlc.narg('requires_shipping')::boolean IS NULL
       OR requires_shipping = sqlc.narg('requires_shipping')::boolean)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

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
