-- notification_deliveries sorguları — TESLİM GÜNLÜĞÜ.
--
-- Günlük iki adımda yazılır: gönderimden ÖNCE kayıt açılır (Claim), gönderimden
-- SONRA sonucu yazılır (Finish). Tek adımda yazmak — yani önce gönderip sonra
-- kaydetmek — mükerrer bildirime kapı açardı: iki eşzamanlı işleyici de
-- sağlayıcıya gider, benzersizlik ihlali ancak İKİ e-posta gittikten sonra
-- görünürdü.

-- ClaimNotificationDelivery kaydı yalnızca o (şablon, referans) çifti HENÜZ
-- KULLANILMAMIŞSA yazar.
--
-- Çakışma hâlinde satır DÖNMEZ (pgx.ErrNoRows) ve bu bir hata değildir:
-- çağıran o zaman gönderimi ATLAR. "Önce oku, yoksa yaz" iki adımı arasına
-- giren eşzamanlı bir çağrı benzersiz indekse çarpardı; ON CONFLICT DO NOTHING
-- yarışı tek deyime indirir ve kazananı veritabanı seçer.
-- name: ClaimNotificationDelivery :one
INSERT INTO notification_deliveries (
    id, template, channel, reference, provider_id, status
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (template, reference) DO NOTHING
RETURNING *;

-- FinishNotificationDelivery gönderim denemesinin sonucunu yazar.
--
-- Durum MUTLAK değerle yazılır, artımlı bir geçişle değil: yazan kod sonucu
-- elinde tutar ve okuduğu satıra göre karar vermez.
-- name: FinishNotificationDelivery :one
UPDATE notification_deliveries
SET status     = $2,
    error      = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetNotificationDelivery :one
SELECT * FROM notification_deliveries
WHERE id = $1;

-- name: ListNotificationDeliveries :many
SELECT * FROM notification_deliveries
WHERE (sqlc.narg('reference')::text IS NULL OR reference = sqlc.narg('reference')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountNotificationDeliveries sayfalama zarfının toplam sayısını verir ve
-- ListNotificationDeliveries ile AYNI süzgeçleri uygular; ikisi birlikte
-- değiştirilmelidir.
--
-- Toplam, satırlarla birlikte dönen bir pencere fonksiyonundan okunamaz:
-- aralık dışı bir sayfada hiç satır dönmez, pencere değerlendirilmez ve toplam
-- 0 görünürdü. Toplam sayfanın değil SÜZGECİN sayısıdır.
-- name: CountNotificationDeliveries :one
SELECT COUNT(*) FROM notification_deliveries
WHERE (sqlc.narg('reference')::text IS NULL OR reference = sqlc.narg('reference')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text);
