-- inventory_reservations sorguları.
--
-- Rezervasyon kaydı SİLİNMEZ, durumu değişir. Telafinin (release) idempotent
-- olması buna dayanır: ikinci çağrı kaydı bulur, "released" görür ve stoğa
-- ikinci kez dokunmadan başarıyla döner.
--
-- Bu yüzden tabloda deleted_at YOKTUR ve okumalar öyle bir süzgeç TAŞIMAZ.
-- Sütun 000001'den beri duruyordu, hiçbir zaman yazılmadı ve her okuma bir kez
-- bile yanlış olmamış bir koşulu taşıyordu; 000002 onu düşürdü. Gerekçe o
-- migration'ın başındadır (docs/gaps.md D18).

-- name: CreateReservation :one
INSERT INTO inventory_reservations (
    id, inventory_item_id, location_id, quantity, line_item_id, status, description
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- LockReservation rezervasyonu işlem boyunca kilitler; durum geçişleri
-- (release/confirm) yalnızca bu kilit altında yapılır.
-- name: LockReservation :one
SELECT * FROM inventory_reservations
WHERE id = $1
FOR UPDATE;

-- name: GetReservation :one
SELECT * FROM inventory_reservations
WHERE id = $1;

-- name: SetReservationStatus :execrows
UPDATE inventory_reservations
SET status = $2, updated_at = now()
WHERE id = $1;

-- name: CountActiveReservationsByItem :one
SELECT COUNT(*) FROM inventory_reservations
WHERE inventory_item_id = $1 AND status = 'active';
