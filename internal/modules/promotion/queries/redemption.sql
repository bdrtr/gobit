-- promotion_redemption sorguları.

-- name: InsertRedemption :one
INSERT INTO promotion_redemption (
    id, promotion_id, campaign_id, reference, amount, currency_code,
    budget_delta, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING *;

-- GetActiveRedemption bir referans için GEÇERLİ (serbest bırakılmamış)
-- kullanımı döner.
--
-- İdempotency'nin okuma tarafıdır: aynı referansla ikinci kez çağrılan
-- RedeemPromotion sayacı artırmak yerine bu kaydı döner.
-- name: GetActiveRedemption :one
SELECT * FROM promotion_redemption
WHERE promotion_id = $1 AND reference = $2 AND released_at IS NULL;

-- LockActiveRedemption GEÇERLİ kullanımı işlem boyunca kilitler.
--
-- Serbest bırakma bu kilidi alır; iki eşzamanlı Release'ten yalnızca biri
-- satırı "released" yazabilir ve diğeri güncellenmiş satırı görüp hiçbir şey
-- yapmaz. Sayacın iki kez düşmesini engelleyen şey budur.
-- name: LockActiveRedemption :one
SELECT * FROM promotion_redemption
WHERE promotion_id = $1 AND reference = $2 AND released_at IS NULL
FOR UPDATE;

-- name: ListRedemptions :many
SELECT * FROM promotion_redemption
WHERE promotion_id = $1
ORDER BY id
LIMIT $2 OFFSET $3;

-- name: CountRedemptions :one
SELECT count(*) FROM promotion_redemption
WHERE promotion_id = $1;

-- MarkRedemptionReleased kullanımı serbest bırakılmış olarak işaretler.
--
-- KOŞUL released_at IS NULL'dır: zaten bırakılmış bir kayıt hiç satır dönmez
-- ve çağıran ikinci düşümü yapmaz. Telafinin idempotent olmasını sağlayan
-- ikinci savunma budur (birincisi satır kilididir).
-- name: MarkRedemptionReleased :one
UPDATE promotion_redemption
SET released_at = $2, updated_at = $2
WHERE id = $1 AND released_at IS NULL
RETURNING *;
