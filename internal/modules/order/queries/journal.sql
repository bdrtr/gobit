-- The rows the order journal is derived from (ADR 0188).
--
-- Each query reads one kind of fact inside a half-open window [from, to), in
-- the order the journal lists it, and takes one row more than the caller's
-- limit so the caller can tell a full window from one that was cut.

-- name: JournalOrdersPlaced :many
SELECT id, currency_code, subtotal, discount_total, tax_total, shipping_total, total, placed_at
FROM orders
WHERE placed_at >= sqlc.arg('from_at') AND placed_at < sqlc.arg('to_at')
  AND (sqlc.narg('currency_code')::text IS NULL OR currency_code = sqlc.narg('currency_code')::text)
ORDER BY placed_at, id
LIMIT sqlc.arg('row_limit');

-- name: JournalOrdersCanceled :many
SELECT id, currency_code, subtotal, discount_total, tax_total, shipping_total, total,
       canceled_at::timestamptz AS canceled_at
FROM orders
WHERE canceled_at >= sqlc.arg('from_at') AND canceled_at < sqlc.arg('to_at')
  AND (sqlc.narg('currency_code')::text IS NULL OR currency_code = sqlc.narg('currency_code')::text)
ORDER BY canceled_at, id
LIMIT sqlc.arg('row_limit');

-- name: JournalCreditLines :many
SELECT cl.id, cl.order_id, cl.amount, cl.created_at, o.currency_code
FROM order_credit_lines cl
JOIN orders o ON o.id = cl.order_id
WHERE cl.created_at >= sqlc.arg('from_at') AND cl.created_at < sqlc.arg('to_at')
  AND (sqlc.narg('currency_code')::text IS NULL OR o.currency_code = sqlc.narg('currency_code')::text)
ORDER BY cl.created_at, cl.id
LIMIT sqlc.arg('row_limit');
