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

-- name: ListStockLocations :many
SELECT * FROM stock_locations
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountStockLocations sayfalama zarfının toplam sayısını verir; ListStockLocations
-- ile aynı filtreyi uygular.
--
-- Sayım ayrı bir sorgudur: satırlarla birlikte dönen bir pencere fonksiyonu,
-- aralık dışı bir sayfada hiç satır dönmediği için toplamı 0 gösterirdi.
-- name: CountStockLocations :one
SELECT COUNT(*) FROM stock_locations
WHERE deleted_at IS NULL;
