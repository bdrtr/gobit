-- payment_gift_card queries — the cards, their ledger and the provider's own
-- sessions (ADR 0208).
--
-- As with store credit, there is no update or delete of a card or an entry: a
-- balance is the sum of what happened to it, and a correction is a new row.

-- InsertGiftCard writes a card. A sold card whose sale already made one writes
-- nothing and returns no row; the caller reads the existing card by its
-- source reference (ADR 0210).
-- name: InsertGiftCard :one
INSERT INTO payment_gift_cards (id, code_digest, code_tail, currency_code, reason, source, source_reference)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (source_reference) WHERE source_reference IS NOT NULL DO NOTHING
RETURNING *;

-- name: GetGiftCardBySourceReference :one
SELECT * FROM payment_gift_cards
WHERE source_reference = $1;

-- ReplaceGiftCardCode gives a card a new code; the balance stays with the card.
-- name: ReplaceGiftCardCode :one
UPDATE payment_gift_cards
SET code_digest = $2, code_tail = $3, code_changed_at = now()
WHERE id = $1
RETURNING *;

-- name: GetGiftCard :one
SELECT * FROM payment_gift_cards
WHERE id = $1;

-- GetGiftCardByDigest finds the card a presented code opens.
-- name: GetGiftCardByDigest :one
SELECT * FROM payment_gift_cards
WHERE code_digest = $1;

-- LockGiftCard locks the card's row for the transaction. It is the balance's
-- lock: unlike a customer's credit, a card always has its row, so the row is
-- there to be locked before the balance is summed.
-- name: LockGiftCard :one
SELECT id FROM payment_gift_cards
WHERE id = $1
FOR UPDATE;

-- name: ListGiftCards :many
SELECT * FROM payment_gift_cards
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- name: CountGiftCards :one
SELECT COUNT(*) FROM payment_gift_cards;

-- name: InsertGiftCardEntry :one
INSERT INTO payment_gift_card_entries (id, gift_card_id, amount, kind, reference)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GiftCardBalance :one
SELECT COALESCE(SUM(amount), 0)::bigint AS balance
FROM payment_gift_card_entries
WHERE gift_card_id = $1;

-- GiftCardBalances sums several cards at once, for a page of the listing.
-- name: GiftCardBalances :many
SELECT gift_card_id, COALESCE(SUM(amount), 0)::bigint AS balance
FROM payment_gift_card_entries
WHERE gift_card_id = ANY(sqlc.arg('ids')::text[])
GROUP BY gift_card_id;

-- name: ListGiftCardEntries :many
SELECT * FROM payment_gift_card_entries
WHERE gift_card_id = $1
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- name: CountGiftCardEntries :one
SELECT COUNT(*) FROM payment_gift_card_entries
WHERE gift_card_id = $1;

-- name: InsertGiftCardSessionIfAbsent :one
INSERT INTO payment_gift_card_sessions (
    id, idempotency_key, reference, gift_card_id, amount, currency_code, status
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: GetGiftCardSession :one
SELECT * FROM payment_gift_card_sessions
WHERE id = $1;

-- name: GetGiftCardSessionByIdempotencyKey :one
SELECT * FROM payment_gift_card_sessions
WHERE idempotency_key = $1;

-- name: LockGiftCardSession :one
SELECT * FROM payment_gift_card_sessions
WHERE id = $1
FOR UPDATE;

-- name: UpdateGiftCardSessionState :one
UPDATE payment_gift_card_sessions
SET status            = $2,
    authorized_amount = $3,
    captured_amount   = $4,
    refunded_amount   = $5,
    decline_reason    = $6,
    updated_at        = now()
WHERE id = $1
RETURNING *;
