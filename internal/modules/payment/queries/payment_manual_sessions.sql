-- payment_manual_sessions queries — the MANUAL provider's own ledger.
--
-- ONLY the manual provider touches this table; the payment service never sees
-- it and reaches the provider only through the PaymentProvider interface. The
-- separation is deliberate: a real payment institution's state is not in the
-- module's database either.

-- InsertManualSessionIfAbsent writes the session only if that idempotency key
-- has NOT BEEN USED YET.
--
-- On a conflict NO row comes back (pgx.ErrNoRows); the caller then reads the
-- session that already exists under the key. A concurrent call slipping between
-- the two steps of "read first, write if absent" would hit the unique index; ON
-- CONFLICT DO NOTHING reduces that race to a single statement.
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

-- LockManualSession locks the session for the length of the transaction; status
-- transitions are made only under this lock. The provider's idempotency
-- requirement rests on it: of two calls authorizing the same session at the
-- same time, the second sees the status the first wrote and does not block the
-- amount A SECOND TIME.
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
