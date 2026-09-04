-- fulfillment_manual_shipments queries — the MANUAL provider's own ledger.
--
-- ONLY the manual provider touches this table; the fulfillment service never
-- sees it and reaches the provider solely through the FulfillmentProvider
-- interface. The separation is deliberate: the state of a real shipping carrier
-- is not in the module's database either.

-- InsertManualShipmentIfAbsent writes the shipment only if that idempotency key
-- has NOT BEEN USED YET.
--
-- On a conflict NO row is returned (pgx.ErrNoRows); the caller then reads the
-- shipment that exists under the key. ON CONFLICT DO NOTHING reduces the race
-- to a single statement; a concurrent call slipping between the two steps of
-- "read first, write if absent" would hit the unique index and abort the
-- transaction.
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

-- LockManualShipment locks the shipment for the duration of the transaction;
-- state transitions are made only under this lock. The provider's idempotency
-- requirement rests on it: of two calls canceling the same shipment at the same
-- time, the second one sees the state the first wrote and does NOT change the
-- ledger A SECOND TIME.
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
