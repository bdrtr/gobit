-- payment_loyalty queries — one table, one writer and two readers.
--
-- There is no update and no delete anywhere in this file, and that is the
-- table's whole design: a balance is the sum of what happened to it, so a
-- correction is a NEW ROW rather than an edited one (ADR 0164, and the module's
-- own rule since its 000003 — "a money record is kept").

-- InsertLoyaltyEntry appends one row to a customer's point ledger.
--
-- The only caller is the service's earn path, which is reached from the one
-- function that moves a collection's totals. Nothing else may write here.
-- name: InsertLoyaltyEntry :one
INSERT INTO payment_loyalty_entries (
    id, customer_id, currency_code, points, kind, reference
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- LoyaltyPointsForReference sums what ONE collection has already been written.
--
-- It is the read that makes the write a target rather than an increment: the
-- earn path computes where this collection's points should be and appends the
-- difference, so a second write for the same totals appends nothing.
--
-- COALESCE because a collection with no rows has earned zero, which is a number
-- rather than an absence.
-- name: LoyaltyPointsForReference :one
SELECT COALESCE(SUM(points), 0)::bigint AS points
FROM payment_loyalty_entries
WHERE reference = $1;

-- LoyaltyBalance sums one customer's points in one currency.
--
-- COALESCE for StoreCreditBalance's reason: a customer who has never earned and
-- a customer whose points were all reversed hold the same number of points.
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

