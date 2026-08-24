-- fulfillment_manual_shipments sorguları — MANUEL sağlayıcının kendi defteri.
--
-- Bu tabloya YALNIZCA manual sağlayıcı dokunur; fulfillment servisi onu hiç
-- görmez ve sağlayıcıya ancak FulfillmentProvider arayüzünden ulaşır. Ayrım
-- bilinçlidir: gerçek bir kargo firmasının durumu da modülün veritabanında
-- değildir.

-- InsertManualShipmentIfAbsent gönderiyi yalnızca o idempotency anahtarı HENÜZ
-- KULLANILMAMIŞSA yazar.
--
-- Çakışma hâlinde satır DÖNMEZ (pgx.ErrNoRows); çağıran o zaman anahtarla var
-- olan gönderiyi okur. ON CONFLICT DO NOTHING yarışı tek deyime indirir;
-- "önce oku, yoksa yaz" iki adımı arasında araya giren eşzamanlı bir çağrı
-- benzersiz indekse çarpıp işlemi iptal ederdi.
-- name: InsertManualShipmentIfAbsent :one
INSERT INTO fulfillment_manual_shipments (
    id, idempotency_key, reference, option_id, status, tracking_number,
    tracking_url, data
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: GetManualShipment :one
SELECT * FROM fulfillment_manual_shipments
WHERE id = $1;

-- name: GetManualShipmentByIdempotencyKey :one
SELECT * FROM fulfillment_manual_shipments
WHERE idempotency_key = $1;

-- LockManualShipment gönderiyi işlem boyunca kilitler; durum geçişleri
-- yalnızca bu kilit altında yapılır. Sağlayıcının idempotency şartı buna
-- dayanır: aynı gönderiyi aynı anda iptal eden iki çağrıdan ikincisi,
-- birincinin yazdığı durumu görür ve defteri İKİNCİ KEZ değiştirmez.
-- name: LockManualShipment :one
SELECT * FROM fulfillment_manual_shipments
WHERE id = $1
FOR UPDATE;

-- name: UpdateManualShipmentState :one
UPDATE fulfillment_manual_shipments
SET status          = $2,
    tracking_number = $3,
    tracking_url    = $4,
    updated_at      = now()
WHERE id = $1
RETURNING *;
