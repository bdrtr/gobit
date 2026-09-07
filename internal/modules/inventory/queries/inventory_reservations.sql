-- inventory_reservations queries.
--
-- A reservation record is NEVER DELETED, its status changes. The compensation
-- (release) being idempotent rests on that: the second call finds the record,
-- sees "released", and returns successfully without touching the stock a second
-- time.
--
-- That is why the table HAS NO deleted_at and the reads CARRY no such filter.
-- The column had been standing there since 000001, was never once written, and
-- every read carried a condition that had not been false a single time; 000002
-- dropped it. The reasoning is at the head of that migration (docs/gaps.md
-- D18).

-- name: CreateReservation :one
INSERT INTO inventory_reservations (
    id, inventory_item_id, location_id, quantity, line_item_id, status, description
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- LockReservation locks the reservation for the duration of the transaction;
-- the status transitions (release/confirm) are made only under this lock.
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
