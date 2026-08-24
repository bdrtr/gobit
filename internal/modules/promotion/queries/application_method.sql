-- promotion_application_method sorguları.

-- UpsertApplicationMethod yöntemi yazar; promosyonun zaten bir yöntemi varsa
-- ÜZERİNE YAZAR.
--
-- Yerine koyma (upsert) bilinçlidir: promosyon başına en fazla bir yöntem
-- vardır ve "önce sil sonra ekle" iki ifade arasında yöntemsiz bir promosyon
-- bırakırdı — o aralıkta koşan bir hesap indirim üretmezdi. Çakışma hedefi
-- kısmi benzersiz indekstir; silinmiş bir yöntem çakışmaya girmez, bu yüzden
-- WHERE koşulu indeksinkiyle birebir aynıdır.
-- name: UpsertApplicationMethod :one
INSERT INTO promotion_application_method (
    id, promotion_id, type, target_type, allocation, value, max_quantity,
    currency_code, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
ON CONFLICT (promotion_id) WHERE deleted_at IS NULL
DO UPDATE SET
    type          = EXCLUDED.type,
    target_type   = EXCLUDED.target_type,
    allocation    = EXCLUDED.allocation,
    value         = EXCLUDED.value,
    max_quantity  = EXCLUDED.max_quantity,
    currency_code = EXCLUDED.currency_code,
    updated_at    = EXCLUDED.updated_at
RETURNING *;

-- name: GetApplicationMethod :one
SELECT * FROM promotion_application_method
WHERE promotion_id = $1 AND deleted_at IS NULL;

-- GetApplicationMethodsByPromotions hesaplamaya giren TÜM promosyonların
-- yöntemlerini tek turda döner; promosyon başına sorgu (N+1) yapılmaz.
-- name: GetApplicationMethodsByPromotions :many
SELECT * FROM promotion_application_method
WHERE promotion_id = ANY (@promotion_ids::text[]) AND deleted_at IS NULL
ORDER BY promotion_id;

-- name: SoftDeleteApplicationMethod :one
UPDATE promotion_application_method
SET deleted_at = $2, updated_at = $2
WHERE promotion_id = $1 AND deleted_at IS NULL
RETURNING id;
