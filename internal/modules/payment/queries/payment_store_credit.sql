-- payment_store_credit queries — the ledger and the provider's own sessions.
--
-- Two tables and two readers. The ENTRIES are the module's own money record: the
-- service issues credit into them and answers a balance out of them. The SESSIONS
-- belong to the store-credit PROVIDER, the way payment_manual_sessions belongs to
-- the manual one, and the service never touches them.

-- InsertStoreCreditEntry appends one event to the ledger.
--
-- There is no update and no delete anywhere in this file, and that is the table's
-- whole design: a balance is the sum of what happened to it, so a correction is a
-- NEW ROW rather than an edited one (the module's own rule since its 000003 —
-- "a money record is kept").
-- name: InsertStoreCreditEntry :one
INSERT INTO payment_store_credit_entries (
    id, customer_id, currency_code, amount, kind, reference, reason
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- StoreCreditBalance sums one customer's entries in one currency.
--
-- COALESCE because a customer with no entries has no rows, and "no rows" is a
-- balance of zero rather than an absence: a shop that has never given somebody
-- credit and a shop that gave and took it back are the same amount of money.
-- name: StoreCreditBalance :one
SELECT COALESCE(SUM(amount), 0)::bigint AS balance
FROM payment_store_credit_entries
WHERE customer_id = $1 AND currency_code = $2;

-- LockStoreCreditEntries takes the customer's rows for the length of the
-- transaction, and it is what makes the balance check safe to act on.
--
-- Two authorizations running at once would otherwise both read a sufficient
-- balance and both write a hold, and the customer would spend money twice. The
-- lock serializes them: the second waits, and the SUM it takes afterwards — a
-- fresh statement, a fresh snapshot — sees the hold the first one wrote.
--
-- A customer with NO entries locks nothing, and that is not a hole: their balance
-- is zero, so no authorization can succeed whatever order the two take.
-- name: LockStoreCreditEntries :many
SELECT id FROM payment_store_credit_entries
WHERE customer_id = $1 AND currency_code = $2
FOR UPDATE;

-- ListStoreCreditEntries returns one customer's history, newest first.
-- name: ListStoreCreditEntries :many
SELECT * FROM payment_store_credit_entries
WHERE customer_id = $1 AND currency_code = $2
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountStoreCreditEntries counts them for the listing's envelope.
-- name: CountStoreCreditEntries :one
SELECT COUNT(*) FROM payment_store_credit_entries
WHERE customer_id = $1 AND currency_code = $2;

-- InsertStoreCreditSessionIfAbsent writes the provider's session only if that
-- idempotency key has not been used yet; see InsertManualSessionIfAbsent for why
-- the conflict is handled in one statement.
-- name: InsertStoreCreditSessionIfAbsent :one
INSERT INTO payment_store_credit_sessions (
    id, idempotency_key, reference, customer_id, amount, currency_code, status
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: GetStoreCreditSession :one
SELECT * FROM payment_store_credit_sessions
WHERE id = $1;

-- name: GetStoreCreditSessionByIdempotencyKey :one
SELECT * FROM payment_store_credit_sessions
WHERE idempotency_key = $1;

-- LockStoreCreditSession locks the session for the length of the transaction;
-- every status transition is made under it, which is what makes a repeated
-- Authorize see what the first one wrote instead of holding the money twice.
-- name: LockStoreCreditSession :one
SELECT * FROM payment_store_credit_sessions
WHERE id = $1
FOR UPDATE;

-- UpdateStoreCreditSessionState writes the status and the three amounts as
-- ABSOLUTE values; an incremental update would pull the value the deciding code
-- saw apart from the value that gets written.
-- name: UpdateStoreCreditSessionState :one
UPDATE payment_store_credit_sessions
SET status            = $2,
    authorized_amount = $3,
    captured_amount   = $4,
    refunded_amount   = $5,
    decline_reason    = $6,
    updated_at        = now()
WHERE id = $1
RETURNING *;
