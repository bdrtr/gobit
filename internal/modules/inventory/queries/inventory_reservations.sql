-- inventory_reservations sorguları.
--
-- Rezervasyon kaydı SİLİNMEZ, durumu değişir. Telafinin (release) idempotent
-- olması buna dayanır: ikinci çağrı kaydı bulur, "released" görür ve stoğa
-- ikinci kez dokunmadan başarıyla döner.

-- name: CreateReservation :one
INSERT INTO inventory_reservations (
    id, inventory_item_id, location_id, quantity, line_item_id, status, description
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- LockReservation rezervasyonu işlem boyunca kilitler; durum geçişleri
-- (release/confirm) yalnızca bu kilit altında yapılır.
-- name: LockReservation :one
SELECT * FROM inventory_reservations
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: GetReservation :one
SELECT * FROM inventory_reservations
WHERE id = $1 AND deleted_at IS NULL;

-- name: SetReservationStatus :execrows
UPDATE inventory_reservations
SET status = $2, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: CountActiveReservationsByItem :one
SELECT COUNT(*) FROM inventory_reservations
WHERE inventory_item_id = $1 AND status = 'active' AND deleted_at IS NULL;
