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
    id, customer_id, currency_code, amount, kind, reference, reason, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, sqlc.narg('expires_at'))
RETURNING *;

-- StoreCreditExpiryFigures reads the four sums the expiry of one balance is
-- decided from (ADR 0258), at the given moment:
--
--   balance    every row;
--   unexpired  the issues not yet expired, and the refunds, which never expire;
--   expired    the issues whose moment has come;
--   written    what expire rows have already taken back, as a positive amount.
--
-- Credit is taken to be spent soonest-expiring first, so the balance is made of
-- the latest money: whatever of it the unexpired sources cannot account for is
-- expired credit, up to what the expired issues gave and has not been taken.
-- The caller holds the balance's lock (LockStoreCreditBalance) while it reads
-- these and writes the row they decide.
-- name: StoreCreditExpiryFigures :one
SELECT
    COALESCE(SUM(amount), 0)::bigint AS balance,
    COALESCE(SUM(amount) FILTER (WHERE kind = 'refund'
        OR (kind = 'issue' AND (expires_at IS NULL OR expires_at > sqlc.arg('at')::timestamptz))), 0)::bigint AS unexpired,
    COALESCE(SUM(amount) FILTER (WHERE kind = 'issue'
        AND expires_at <= sqlc.arg('at')::timestamptz), 0)::bigint AS expired,
    COALESCE(-SUM(amount) FILTER (WHERE kind = 'expire'), 0)::bigint AS written
FROM payment_store_credit_entries
WHERE customer_id = sqlc.arg('customer_id') AND currency_code = sqlc.arg('currency_code');

-- StoreCreditExpiryDue lists the balances an expiry is owed from, at the given
-- moment, at most row_limit of them: those holding an expired issue whose
-- figures still leave something to take back (StoreCreditExpiryFigures). The
-- inner read walks the partial index of expiring issues, so a balance that
-- never had one is never summed.
-- name: StoreCreditExpiryDue :many
WITH candidates AS (
    SELECT DISTINCT customer_id, currency_code
    FROM payment_store_credit_entries
    WHERE kind = 'issue' AND expires_at IS NOT NULL
      AND expires_at <= sqlc.arg('at')::timestamptz
), figures AS (
    SELECT e.customer_id, e.currency_code,
        SUM(e.amount) AS balance,
        COALESCE(SUM(e.amount) FILTER (WHERE e.kind = 'refund'
            OR (e.kind = 'issue' AND (e.expires_at IS NULL OR e.expires_at > sqlc.arg('at')::timestamptz))), 0) AS unexpired,
        COALESCE(SUM(e.amount) FILTER (WHERE e.kind = 'issue'
            AND e.expires_at <= sqlc.arg('at')::timestamptz), 0) AS expired,
        COALESCE(-SUM(e.amount) FILTER (WHERE e.kind = 'expire'), 0) AS written
    FROM payment_store_credit_entries e
    JOIN candidates c ON c.customer_id = e.customer_id AND c.currency_code = e.currency_code
    GROUP BY e.customer_id, e.currency_code
)
SELECT customer_id, currency_code
FROM figures
WHERE LEAST(GREATEST(balance - unexpired, 0), expired - written) > 0
ORDER BY customer_id, currency_code
LIMIT sqlc.arg('row_limit')::bigint;

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
    id, idempotency_key, reference, customer_id, amount, currency_code, status, partial
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
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
