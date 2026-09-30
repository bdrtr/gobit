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

-- JournalStoreCreditIssues reads the store credit the shop gave, and what an
-- expiry took back of it (ADR 0258); the kind tells the two apart.
-- name: JournalStoreCreditIssues :many
SELECT id, customer_id, currency_code, amount, kind, created_at
FROM payment_store_credit_entries
WHERE kind IN ('issue', 'expire')
  AND created_at >= sqlc.arg('from_at') AND created_at < sqlc.arg('to_at')
  AND (sqlc.narg('currency_code')::text IS NULL OR currency_code = sqlc.narg('currency_code')::text)
ORDER BY created_at, id
LIMIT sqlc.arg('row_limit');

-- A gift card's issue is read with the card's currency, which the entry does not
-- repeat (ADR 0208). Only an ISSUED card's is: a sold card is the order's sale,
-- and the order's books hold it (ADR 0210).
-- name: JournalGiftCardIssues :many
SELECT e.id, e.gift_card_id, g.currency_code, e.amount, e.created_at
FROM payment_gift_card_entries e
JOIN payment_gift_cards g ON g.id = e.gift_card_id
WHERE e.kind = 'issue' AND g.source = 'issued'
  AND e.created_at >= sqlc.arg('from_at') AND e.created_at < sqlc.arg('to_at')
  AND (sqlc.narg('currency_code')::text IS NULL OR g.currency_code = sqlc.narg('currency_code')::text)
ORDER BY e.created_at, e.id
LIMIT sqlc.arg('row_limit');

-- A closed card's void is read with the card's source, which decides where the
-- balance goes: a granted card's cost comes back, a sold card's price is kept
-- (ADR 0213).
-- name: JournalGiftCardVoids :many
SELECT e.id, e.gift_card_id, g.currency_code, g.source, e.amount, e.created_at
FROM payment_gift_card_entries e
JOIN payment_gift_cards g ON g.id = e.gift_card_id
WHERE e.kind = 'void'
  AND e.created_at >= sqlc.arg('from_at') AND e.created_at < sqlc.arg('to_at')
  AND (sqlc.narg('currency_code')::text IS NULL OR g.currency_code = sqlc.narg('currency_code')::text)
ORDER BY e.created_at, e.id
LIMIT sqlc.arg('row_limit');

-- name: JournalLoyaltyGrants :many
SELECT id, customer_id, currency_code, points, kind, created_at
FROM payment_loyalty_entries
WHERE kind IN ('earn', 'reverse')
  AND created_at >= sqlc.arg('from_at') AND created_at < sqlc.arg('to_at')
  AND (sqlc.narg('currency_code')::text IS NULL OR currency_code = sqlc.narg('currency_code')::text)
ORDER BY created_at, id
LIMIT sqlc.arg('row_limit');

-- The refunds that name a cause inside a window (ADR 0189): what the order
-- module reads back into the revenue a return or a claim gave back.
-- name: CausedRefunds :many
SELECT r.id, r.reference, r.amount, r.created_at, p.currency_code, p.payment_collection_id
FROM refunds r
JOIN payments p ON p.id = r.payment_id
WHERE r.reference <> ''
  AND r.created_at >= sqlc.arg('from_at') AND r.created_at < sqlc.arg('to_at')
  AND (sqlc.narg('currency_code')::text IS NULL OR p.currency_code = sqlc.narg('currency_code')::text)
ORDER BY r.created_at, r.id
LIMIT sqlc.arg('row_limit');
