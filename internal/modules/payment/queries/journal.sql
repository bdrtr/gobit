-- The rows the payment journal is derived from (ADR 0186).
--
-- Each query reads one kind of movement inside a half-open window [from, to),
-- in the order the journal lists it, and takes one row more than the caller's
-- limit so the caller can tell a full window from one that was cut.

-- name: JournalCaptures :many
SELECT p.id, p.amount, p.currency_code, p.captured_at, p.payment_collection_id,
       s.provider_id, c.customer_id
FROM payments p
JOIN payment_sessions s ON s.id = p.payment_session_id
JOIN payment_collections c ON c.id = p.payment_collection_id
WHERE p.captured_at >= sqlc.arg('from_at') AND p.captured_at < sqlc.arg('to_at')
  AND (sqlc.narg('currency_code')::text IS NULL OR p.currency_code = sqlc.narg('currency_code')::text)
ORDER BY p.captured_at, p.id
LIMIT sqlc.arg('row_limit');

-- A refund has no currency of its own: it is in the currency of the capture it
-- gives back.
-- name: JournalRefunds :many
SELECT r.id, r.amount, r.created_at, p.currency_code, p.payment_collection_id,
       s.provider_id, c.customer_id
FROM refunds r
JOIN payments p ON p.id = r.payment_id
JOIN payment_sessions s ON s.id = p.payment_session_id
JOIN payment_collections c ON c.id = p.payment_collection_id
WHERE r.created_at >= sqlc.arg('from_at') AND r.created_at < sqlc.arg('to_at')
  AND (sqlc.narg('currency_code')::text IS NULL OR p.currency_code = sqlc.narg('currency_code')::text)
ORDER BY r.created_at, r.id
LIMIT sqlc.arg('row_limit');

-- name: JournalStoreCreditIssues :many
SELECT id, customer_id, currency_code, amount, created_at
FROM payment_store_credit_entries
WHERE kind = 'issue'
  AND created_at >= sqlc.arg('from_at') AND created_at < sqlc.arg('to_at')
  AND (sqlc.narg('currency_code')::text IS NULL OR currency_code = sqlc.narg('currency_code')::text)
ORDER BY created_at, id
LIMIT sqlc.arg('row_limit');

-- name: JournalLoyaltyGrants :many
SELECT id, customer_id, currency_code, points, kind, created_at
FROM payment_loyalty_entries
WHERE kind IN ('earn', 'reverse')
  AND created_at >= sqlc.arg('from_at') AND created_at < sqlc.arg('to_at')
  AND (sqlc.narg('currency_code')::text IS NULL OR currency_code = sqlc.narg('currency_code')::text)
ORDER BY created_at, id
LIMIT sqlc.arg('row_limit');
