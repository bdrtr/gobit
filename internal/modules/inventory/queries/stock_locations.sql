-- stock_locations sorguları.
-- Tüm okumalar deleted_at IS NULL filtresi uygular (plan Bölüm 8).

-- name: CreateStockLocation :one
INSERT INTO stock_locations (
    id, name, address_1, address_2, city, province, postal_code, country_code
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetStockLocation :one
SELECT * FROM stock_locations
WHERE id = $1 AND deleted_at IS NULL;

-- Toplam sayı, sayfalama zarfının count alanı için pencere fonksiyonuyla aynı
-- sorguda hesaplanır: pencere LIMIT'ten ÖNCE değerlendirildiği için sayfadaki
-- satır sayısını değil, filtreye uyan TÜM satırların sayısını verir.
-- name: ListStockLocations :many
SELECT *, COUNT(*) OVER () AS total_count
FROM stock_locations
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;
