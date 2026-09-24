-- payment_loyalty queries — the point ledger and the tender's own sessions.
--
-- The LEDGER has two writers and an arch gate holds the pair: the service's
-- earn path writes an earn or a reverse, and the loyalty-points provider writes
-- a hold, a release or a refund (ADR 0165). There is no UPDATE and no DELETE
-- against the ledger anywhere in this file, and that is the table's whole
-- design: a balance is the sum of what happened to it, so a correction is a NEW
-- ROW rather than an edited one (ADR 0164, and the module's own rule since its
-- 000003 — "a money record is kept"). The SESSIONS belong to the provider, the
-- way payment_store_credit_sessions belong to the store-credit one.

-- InsertLoyaltyEntry appends one row to a customer's point ledger.
--
-- Two callers and no third: the service's earn path, reached from the one
-- function that moves a collection's totals, and the loyalty-points provider's
-- package. The arch gate derives the second from the provider's identity.
-- name: InsertLoyaltyEntry :one
INSERT INTO payment_loyalty_entries (
    id, customer_id, currency_code, points, kind, reference
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- LoyaltyPointsForReference sums what ONE collection has already been EARNED.
--
-- It is the read that makes the write a target rather than an increment: the
-- earn path computes where this collection's points should be and appends the
-- difference, so a second write for the same totals appends nothing.
--
-- Only the two earning kinds are summed. A spend row references the provider's
-- own session and never a collection, so the filter changes no sum today; it is
-- here so that the arithmetic states its subject — what this collection EARNED
-- — instead of resting on a convention kept in another package (ADR 0165).
--
-- COALESCE because a collection with no rows has earned zero, which is a number
-- rather than an absence.
-- name: LoyaltyPointsForReference :one
SELECT COALESCE(SUM(points), 0)::bigint AS points
FROM payment_loyalty_entries
WHERE reference = $1 AND kind IN ('earn', 'reverse');

-- LoyaltyBalance sums one customer's points in one currency.
--
-- COALESCE for StoreCreditBalance's reason: a customer who has never earned and
-- a customer whose points were all reversed hold the same number of points.
--
-- The lock that makes this sum safe to act on is not a query in this file. A sum
-- has no row to lock, so the tender takes an advisory lock keyed on the customer
-- and the currency before reading it (repository/ledgerlock.go, D118).
-- name: LoyaltyBalance :one
SELECT COALESCE(SUM(points), 0)::bigint AS points
FROM payment_loyalty_entries
WHERE customer_id = $1 AND currency_code = $2;

-- ListLoyaltyEntries returns one customer's history, newest first.
-- name: ListLoyaltyEntries :many
SELECT * FROM payment_loyalty_entries
WHERE customer_id = $1 AND currency_code = $2
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountLoyaltyEntries counts them for the listing's envelope.
-- name: CountLoyaltyEntries :one
SELECT COUNT(*) FROM payment_loyalty_entries
WHERE customer_id = $1 AND currency_code = $2;

-- InsertLoyaltySessionIfAbsent writes the provider's session only if that
-- idempotency key has not been used yet; see InsertManualSessionIfAbsent for why
-- the conflict is handled in one statement.
-- name: InsertLoyaltySessionIfAbsent :one
INSERT INTO payment_loyalty_sessions (
    id, idempotency_key, reference, customer_id, amount, currency_code, status
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: GetLoyaltySession :one
SELECT * FROM payment_loyalty_sessions
WHERE id = $1;

-- name: GetLoyaltySessionByIdempotencyKey :one
SELECT * FROM payment_loyalty_sessions
WHERE idempotency_key = $1;

-- LockLoyaltySession locks the session for the length of the transaction; every
-- status transition is made under it, which is what makes a repeated Authorize
-- see what the first one wrote instead of holding the points twice.
-- name: LockLoyaltySession :one
SELECT * FROM payment_loyalty_sessions
WHERE id = $1
FOR UPDATE;

-- UpdateLoyaltySessionState writes the status and the three amounts as ABSOLUTE
-- values; an incremental update would pull the value the deciding code saw
-- apart from the value that gets written.
-- name: UpdateLoyaltySessionState :one
UPDATE payment_loyalty_sessions
SET status            = $2,
    authorized_amount = $3,
    captured_amount   = $4,
    refunded_amount   = $5,
    decline_reason    = $6,
    updated_at        = now()
WHERE id = $1
RETURNING *;
