-- inventory_levels sorguları.
--
-- Satılabilir adet (available) hiçbir sorguda SAKLANMIŞ bir sütundan okunmaz;
-- her yerde stocked_quantity - reserved_quantity olarak türetilir.

-- LockInventoryLevel seviye satırını işlem boyunca kilitler.
--
-- EŞZAMANLILIĞIN TEMELİ BUDUR. İki eşzamanlı rezervasyon aynı satırı kilitlemek
-- zorundadır; ikincisi birincinin işlemi bitene kadar bekler ve READ COMMITTED
-- altında satırın GÜNCEL sürümünü görür. "Önce oku sonra yaz" yarışı bu yüzden
-- oluşamaz: okuma zaten kilidin ardından yapılır.
-- name: LockInventoryLevel :one
SELECT * FROM inventory_levels
WHERE inventory_item_id = $1 AND location_id = $2 AND deleted_at IS NULL
FOR UPDATE;

-- name: CreateInventoryLevel :one
INSERT INTO inventory_levels (
    id, inventory_item_id, location_id, stocked_quantity, reserved_quantity
) VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateInventoryLevelQuantities :one
UPDATE inventory_levels
SET stocked_quantity = $2, reserved_quantity = $3, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: ListInventoryLevels :many
SELECT * FROM inventory_levels
WHERE inventory_item_id = $1 AND deleted_at IS NULL
ORDER BY created_at, id;

-- AvailableQuantityByItemIDs kalem başına TÜM lokasyonların satılabilir
-- toplamını tek turda döner; Query sağlayıcısı bunu kullanır.
-- Hiç seviyesi olmayan kalem için satır DÖNMEZ; çağıran onu sıfır sayar.
-- name: AvailableQuantityByItemIDs :many
SELECT inventory_item_id,
       COALESCE(SUM(stocked_quantity - reserved_quantity), 0)::bigint AS available_quantity
FROM inventory_levels
WHERE inventory_item_id = ANY (sqlc.arg('ids')::text[]) AND deleted_at IS NULL
GROUP BY inventory_item_id;

-- name: SoftDeleteInventoryLevelsByItem :exec
UPDATE inventory_levels
SET deleted_at = now(), updated_at = now()
WHERE inventory_item_id = $1 AND deleted_at IS NULL;
