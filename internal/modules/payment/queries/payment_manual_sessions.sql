-- payment_manual_sessions sorguları — MANUEL sağlayıcının kendi defteri.
--
-- Bu tabloya YALNIZCA manual sağlayıcı dokunur; payment servisi onu hiç
-- görmez ve sağlayıcıya ancak PaymentProvider arayüzünden ulaşır. Ayrım
-- bilinçlidir: gerçek bir ödeme kuruluşunun durumu da modülün veritabanında
-- değildir.

-- InsertManualSessionIfAbsent oturumu yalnızca o idempotency anahtarı HENÜZ
-- KULLANILMAMIŞSA yazar.
--
-- Çakışma hâlinde satır DÖNMEZ (pgx.ErrNoRows); çağıran o zaman anahtarla
-- var olan oturumu okur. "Önce oku, yoksa yaz" iki adımı arasında araya giren
-- eşzamanlı bir çağrı benzersiz indekse çarpardı; ON CONFLICT DO NOTHING bu
-- yarışı tek deyime indirir.
-- name: InsertManualSessionIfAbsent :one
INSERT INTO payment_manual_sessions (
    id, idempotency_key, reference, amount, currency_code, status, data
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: GetManualSession :one
SELECT * FROM payment_manual_sessions
WHERE id = $1;

-- name: GetManualSessionByIdempotencyKey :one
SELECT * FROM payment_manual_sessions
WHERE idempotency_key = $1;

-- LockManualSession oturumu işlem boyunca kilitler; durum geçişleri yalnızca
-- bu kilit altında yapılır. Sağlayıcının idempotency şartı buna dayanır: aynı
-- oturumu aynı anda yetkilendiren iki çağrıdan ikincisi, birincinin yazdığı
-- durumu görür ve tutarı İKİNCİ KEZ bloke etmez.
-- name: LockManualSession :one
SELECT * FROM payment_manual_sessions
WHERE id = $1
FOR UPDATE;

-- name: UpdateManualSessionState :one
UPDATE payment_manual_sessions
SET status            = $2,
    authorized_amount = $3,
    captured_amount   = $4,
    refunded_amount   = $5,
    decline_reason    = $6,
    updated_at        = now()
WHERE id = $1
RETURNING *;
