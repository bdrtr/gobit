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
-- 'pending' is LEFT OUT, and that is deliberate: before a session of a driven
-- provider is authorized no money has moved, so there is no amount that could
-- diverge either. Widening the scope to there would mean asking the provider
-- about every cart that was ever opened and abandoned — that is, putting the
-- noise in front of the row that has to be looked at. A provider whose customer
-- pays on its own page moves money while the session is pending, and its
-- pending sessions are read by ListPendingSessionsForReconciliation, on a page
-- of their own, so they cannot crowd this one (ADR 0438).
--
-- $2 is a WAITING PERIOD, not an optional threshold: a capture in flight stands
-- in exactly this state for seconds at a time, and counting that as a
-- divergence would drop every normal payment into the report.
--
-- The providers whose money arrives after the order is placed are left out
-- (ADR 0284): an offline session stays authorized for as long as the customer
-- takes to pay, which is that provider working rather than a capture in
-- flight, and the oldest-first page would fill with them and never reach a
-- card's session.
-- name: ListSessionsForReconciliation :many
SELECT * FROM payment_sessions
WHERE status = 'authorized'
  AND updated_at < sqlc.arg(unchanged_since)
  AND provider_id <> ALL(sqlc.arg(excluded_providers)::text[])
ORDER BY updated_at
LIMIT sqlc.arg(row_limit);

-- ListPendingSessionsForReconciliation returns the PENDING and CANCELED
-- sessions of the providers whose customer pays on the provider's own page
-- (ADR 0438) that settled — were last written before unchanged_since — no
-- earlier than settled_after, newest first.
--
-- # Why pending, for these providers only
--
-- ListSessionsForReconciliation leaves pending out because before an
-- authorization no money has moved. That premise holds for a provider the
-- module drives and fails for one whose customer pays on its page: the session
-- is opened before the cart is completed, the money moves when the customer
-- pays, and the session stays pending here until a completion authorizes it. A
-- customer who pays and closes the browser leaves money taken, a pending
-- session and no order, and this is the only read that reaches it. Canceled
-- too, because such a provider cannot void the page it handed out: a customer
-- who pays after the session was canceled leaves money taken on a canceled
-- session, which nothing else reads.
--
-- # Why a window, newest first
--
-- Most of these sessions are a checkout the shopper left without paying, and
-- they stay pending for good. Read with no lower bound, oldest first, the page
-- would fill with them and never reach a payment made today; newest first with
-- a fixed page, a burst of abandoned pages would push a paid one off the page
-- before any pass reached it. Read as the window of the last settled day, every
-- session in it is asked about on every pass for that day, and a window larger
-- than the page is reported as truncated rather than read in part in silence.
-- name: ListPendingSessionsForReconciliation :many
SELECT * FROM payment_sessions
WHERE status IN ('pending', 'canceled')
  AND updated_at < sqlc.arg(unchanged_since)
  AND updated_at >= sqlc.arg(settled_after)
  AND provider_id = ANY(sqlc.arg(provider_ids)::text[])
ORDER BY updated_at DESC
LIMIT sqlc.arg(row_limit);

-- The offline sessions past the wait their method was given (ADR 0289):
-- authorized, of one of the named providers, opened before that provider's
-- cutoff, in a collection that captured nothing — a collection that took a
-- gift card beside the transfer is the shop's to decide, and its order's
-- cancel would be refused anyway.
--
-- The cutoff is the provider's own: provider_ids and cutoffs are parallel
-- arrays, and array_position finds the row's provider in the first. The page
-- is keyed on (created_at, id), oldest first, so an order the job cannot
-- cancel does not hold the page against the ones after it.
-- name: ListOverdueOfflineSessions :many
SELECT * FROM payment_sessions s
WHERE s.status = 'authorized'
  AND s.provider_id = ANY(sqlc.arg(provider_ids)::text[])
  AND s.created_at < (sqlc.arg(cutoffs)::timestamptz[])[array_position(sqlc.arg(provider_ids)::text[], s.provider_id)]
  AND (s.created_at, s.id) > (sqlc.arg(after_created_at)::timestamptz, sqlc.arg(after_id)::text)
  AND EXISTS (
    SELECT 1 FROM payment_collections c
    WHERE c.id = s.payment_collection_id AND c.captured_amount = 0
  )
ORDER BY s.created_at, s.id
LIMIT sqlc.arg(row_limit);

-- ListSessionsByExternalID returns the sessions a provider knows by one id of
-- its own: what a provider's report names (ADR 0438). A PayTR order id is a
-- collection's, so more than one session may carry it.
-- name: ListSessionsByExternalID :many
SELECT * FROM payment_sessions
WHERE provider_id = sqlc.arg(provider_id) AND external_id = sqlc.arg(external_id)
ORDER BY created_at, id;

-- RecordSessionReport opens the report of a session once, first due at the
-- given moment; a second report of the same session changes nothing, and says
-- so by touching no row (ADR 0438).
-- name: RecordSessionReport :execrows
INSERT INTO payment_session_reports (session_id, next_attempt_at)
VALUES (sqlc.arg(session_id), sqlc.arg(first_due))
ON CONFLICT (session_id) DO NOTHING;

-- ListDueSessionReports returns the open reports whose next attempt is due,
-- soonest first.
-- name: ListDueSessionReports :many
SELECT * FROM payment_session_reports
WHERE outcome = '' AND next_attempt_at <= sqlc.arg(due_by)
ORDER BY next_attempt_at, session_id
LIMIT sqlc.arg(row_limit);

-- GetSessionReport returns one report.
-- name: GetSessionReport :one
SELECT * FROM payment_session_reports WHERE session_id = sqlc.arg(session_id);

-- RecordSessionReportAttempt counts a failed attempt of an open report and
-- sets when the next one is due.
-- name: RecordSessionReportAttempt :execrows
UPDATE payment_session_reports
SET attempts = attempts + 1,
    next_attempt_at = sqlc.arg(next_attempt_at),
    last_failure = sqlc.arg(last_failure)
WHERE session_id = sqlc.arg(session_id) AND outcome = '';

-- RecordSessionAnswer keeps the digest of the secret an opened payment hands
-- its storefront (ADR 0438).
-- name: RecordSessionAnswer :exec
INSERT INTO payment_session_answers (session_id, secret_digest)
VALUES (sqlc.arg(session_id), sqlc.arg(secret_digest));

-- ClaimSessionAnswer spends the secret: it marks the session answered when the
-- digest is its own and it was never answered, and touches no row otherwise
-- (ADR 0438, ADR 0439).
-- name: ClaimSessionAnswer :execrows
UPDATE payment_session_answers
SET answered_at = now()
WHERE session_id = sqlc.arg(session_id)
  AND secret_digest = sqlc.arg(secret_digest)
  AND answered_at IS NULL;

-- SettleSessionReport closes an open report with its outcome; a settled report
-- is not settled again.
-- name: SettleSessionReport :execrows
UPDATE payment_session_reports
SET outcome = sqlc.arg(outcome),
    settled_at = now(),
    last_failure = sqlc.arg(note)
WHERE session_id = sqlc.arg(session_id) AND outcome = '';
