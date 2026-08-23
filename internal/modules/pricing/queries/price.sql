-- name: InsertPrice :one
INSERT INTO price (
    id, price_set_id, price_list_id, currency_code,
    amount, min_quantity, max_quantity, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING *;

-- name: GetPrice :one
SELECT * FROM price
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListPricesBySet :many
SELECT * FROM price
WHERE price_set_id = $1 AND deleted_at IS NULL
ORDER BY id;

-- name: ListPricesBySets :many
SELECT * FROM price
WHERE price_set_id = ANY(@price_set_ids::text[]) AND deleted_at IS NULL
ORDER BY price_set_id, id;

-- name: SoftDeletePricesBySet :exec
UPDATE price
SET deleted_at = $2, updated_at = $2
WHERE price_set_id = $1 AND deleted_at IS NULL;

-- ListPriceCandidates bir price set'in TÜM fiyatlarını, bağlı oldukları fiyat
-- listesinin üstverisiyle birlikte döner.
--
-- Para birimi, adet aralığı ve liste geçerliliği burada ELENMEZ: seçim kuralının
-- her dalı servis katmanındaki saf fonksiyonda yaşar ve veritabanı olmadan
-- birim testiyle kanıtlanabilir. LEFT JOIN'in deleted_at koşulu tabloya değil
-- JOIN'e yazılır; silinmiş bir listeye bağlı fiyat böylece satırını korur ama
-- liste üstverisi NULL gelir ve servis onu eleyebilir.
-- name: ListPriceCandidates :many
SELECT
    p.*,
    pl.id        AS list_id,
    pl.type      AS list_type,
    pl.status    AS list_status,
    pl.starts_at AS list_starts_at,
    pl.ends_at   AS list_ends_at
FROM price p
LEFT JOIN price_list pl
       ON pl.id = p.price_list_id AND pl.deleted_at IS NULL
WHERE p.price_set_id = $1 AND p.deleted_at IS NULL
ORDER BY p.id;
