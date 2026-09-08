-- payment_sessions queries.
--
-- A session record is NEVER DELETED, its status changes. The idempotence of the
-- compensation (CancelPayment) rests on that: the second call finds the record,
-- sees "canceled" and returns successfully without going to the provider a
-- second time. A deleted session and a session that never existed could not be
-- told apart.
--
-- The schema now says the same thing: there is no deleted_at to write and no
-- read here filters on one (ADR 0054).

-- name: CreatePaymentSession :one
INSERT INTO payment_sessions (
    id, payment_collection_id, provider_id, external_id, status,
    amount, authorized_amount, currency_code, data, idempotency_key
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetPaymentSession :one
SELECT * FROM payment_sessions
WHERE id = $1;

-- LockPaymentSession locks the session for the length of the transaction;
-- status transitions (authorize/capture/cancel) are made only under this lock.
-- Of two calls trying to authorize the same session at the same time, the
-- second sees the status the first wrote and does NOT go to the provider A
-- SECOND TIME.
-- name: LockPaymentSession :one
SELECT * FROM payment_sessions
WHERE id = $1
FOR UPDATE;

-- GetPaymentSessionByIdempotencyKey finds the session opened with the same key.
-- CreateSession asks this BEFORE IT GOES to the provider; a second call opens
-- no new session (plan Section 2.6, the core/provider idempotency requirement).
-- name: GetPaymentSessionByIdempotencyKey :one
SELECT * FROM payment_sessions
WHERE provider_id = $1 AND idempotency_key = $2;

-- name: ListPaymentSessionsByCollection :many
SELECT * FROM payment_sessions
WHERE payment_collection_id = $1
ORDER BY created_at DESC, id DESC;

-- CountPaymentSessionStates counts a collection's sessions by status in a
-- SINGLE query. The collection's derived status looks at these counts: a
-- collection with no session at all is "not_paid", one with a live session is
-- "awaiting", one whose only sessions are canceled is "canceled" (see
-- service.CollectionStatusFor).
-- name: CountPaymentSessionStates :one
SELECT
    COUNT(*) FILTER (WHERE status IN ('pending', 'authorized')) AS live_count,
    COUNT(*) FILTER (WHERE status = 'canceled')                 AS canceled_count,
    COUNT(*) FILTER (WHERE status = 'failed')                   AS failed_count,
    COUNT(*)                                                    AS total_count
FROM payment_sessions
WHERE payment_collection_id = $1;

-- SumLiveSessionAmounts gives the total amount the collection's LIVE sessions
-- have reserved. The amount left for a new session to claim is computed from
-- it.
--
-- A pending session reserves its own AMOUNT: it has not been authorized yet,
-- but when it is it can block that amount in full. An authorized session, on
-- the other hand, holds only WHAT IT BLOCKED; since it cannot be authorized a
-- second time (see models.SessionStatus.AuthorizeAction), the remainder will
-- never be used again. A computation that looked only at the authorized amount
-- would allow two FULL-amount sessions, neither of them authorized, to be
-- opened on the same collection — and, once both were authorized, permit a
-- DOUBLE CHARGE.
-- name: SumLiveSessionAmounts :one
SELECT COALESCE(SUM(
    CASE WHEN status = 'pending' THEN amount ELSE authorized_amount END
), 0)::bigint AS reserved_amount
FROM payment_sessions
WHERE payment_collection_id = $1
  AND status IN ('pending', 'authorized');

-- UpdatePaymentSessionState writes the session's status, its authorized amount,
-- the raw provider data and the decline reason as ABSOLUTE values.
-- name: UpdatePaymentSessionState :one
UPDATE payment_sessions
SET status            = $2,
    authorized_amount = $3,
    data              = $4,
    decline_reason    = $5,
    updated_at        = now()
WHERE id = $1
RETURNING *;

-- ListSessionsForReconciliation returns the sessions the provider has to be
-- ASKED about: the ones that look authorized but not captured, and have looked
-- that way for a while.
--
-- # Why exactly this set
--
-- The module makes its provider call INSIDE ITS OWN transaction. If the
-- transaction is rolled back after the money was taken, the session stays
-- 'authorized' locally while at the provider it is 'captured' — and that
-- difference is visible from nowhere else (see
-- internal/workflows/checkout/doc.go, "REMAINING RISK").
--
-- 'pending' is LEFT OUT, and that is deliberate: before a session is authorized
-- no money has moved, so there is no amount that could diverge either. Widening
-- the scope to there would mean asking the provider about every cart that was
-- ever opened and abandoned — that is, putting the noise in front of the row
-- that has to be looked at.
--
-- $2 is a WAITING PERIOD, not an optional threshold: a capture in flight stands
-- in exactly this state for seconds at a time, and counting that as a
-- divergence would drop every normal payment into the report.
-- name: ListSessionsForReconciliation :many
SELECT * FROM payment_sessions
WHERE status = 'authorized'
  AND updated_at < $1
ORDER BY updated_at
LIMIT $2;
