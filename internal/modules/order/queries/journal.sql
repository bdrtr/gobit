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

-- A credit line a delivery change wrote names the change (ADR 0199): the
-- journal books it against shipping rather than as a concession.
-- name: JournalCreditLines :many
SELECT cl.id, cl.order_id, cl.amount, cl.created_at, o.currency_code,
       dc.id AS delivery_change_id
FROM order_credit_lines cl
JOIN orders o ON o.id = cl.order_id
LEFT JOIN order_delivery_changes dc ON dc.credit_line_id = cl.id
WHERE cl.created_at >= sqlc.arg('from_at') AND cl.created_at < sqlc.arg('to_at')
  AND (sqlc.narg('currency_code')::text IS NULL OR o.currency_code = sqlc.narg('currency_code')::text)
ORDER BY cl.created_at, cl.id
LIMIT sqlc.arg('row_limit');

-- The order records a refund can name as its cause (ADR 0189): which order a
-- return or a claim belongs to, and that order's currency.
-- name: JournalCauses :many
SELECT r.id, 'return'::text AS kind, r.order_id, o.currency_code
FROM order_returns r
JOIN orders o ON o.id = r.order_id
WHERE r.id = ANY (sqlc.arg('ids')::text[])
UNION ALL
SELECT c.id, 'claim'::text, c.order_id, o.currency_code
FROM order_claims c
JOIN orders o ON o.id = c.order_id
WHERE c.id = ANY (sqlc.arg('ids')::text[]);
