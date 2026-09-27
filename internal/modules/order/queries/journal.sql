-- The rows the order journal is derived from (ADR 0188).
--
-- Each query reads one kind of fact inside a half-open window [from, to), in
-- the order the journal lists it, and takes one row more than the caller's
-- limit so the caller can tell a full window from one that was cut.

-- An order's gift card subtotal is the price of the lines that sold gift cards,
-- which the books hold as a debt rather than as sales (ADR 0211).
-- name: JournalOrdersPlaced :many
SELECT o.id, o.currency_code, o.subtotal, o.discount_total, o.tax_total, o.shipping_total, o.total, o.placed_at,
       (SELECT COALESCE(SUM(li.subtotal), 0)::bigint FROM order_line_items li
        WHERE li.order_id = o.id AND li.is_giftcard) AS gift_card_subtotal
FROM orders o
WHERE o.placed_at >= sqlc.arg('from_at') AND o.placed_at < sqlc.arg('to_at')
  AND (sqlc.narg('currency_code')::text IS NULL OR o.currency_code = sqlc.narg('currency_code')::text)
ORDER BY o.placed_at, o.id
LIMIT sqlc.arg('row_limit');

-- name: JournalOrdersCanceled :many
SELECT o.id, o.currency_code, o.subtotal, o.discount_total, o.tax_total, o.shipping_total, o.total,
       o.canceled_at::timestamptz AS canceled_at,
       (SELECT COALESCE(SUM(li.subtotal), 0)::bigint FROM order_line_items li
        WHERE li.order_id = o.id AND li.is_giftcard) AS gift_card_subtotal
FROM orders o
WHERE o.canceled_at >= sqlc.arg('from_at') AND o.canceled_at < sqlc.arg('to_at')
  AND (sqlc.narg('currency_code')::text IS NULL OR o.currency_code = sqlc.narg('currency_code')::text)
ORDER BY o.canceled_at, o.id
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

-- A dearer delivery change and what it added to what the order owes
-- (ADR 0200). A cheaper one is read through its credit line above.
-- name: JournalDeliveryUpgrades :many
SELECT dc.id, dc.order_id, dc.difference, dc.created_at, o.currency_code
FROM order_delivery_changes dc
JOIN orders o ON o.id = dc.order_id
WHERE dc.difference > 0
  AND dc.created_at >= sqlc.arg('from_at') AND dc.created_at < sqlc.arg('to_at')
  AND (sqlc.narg('currency_code')::text IS NULL OR o.currency_code = sqlc.narg('currency_code')::text)
ORDER BY dc.created_at, dc.id
LIMIT sqlc.arg('row_limit');

-- An exchange whose difference was collected, at the moment it was funded
-- (ADR 0203). funded_at survives the withdrawal that sends the money back, so
-- the entry stays where it was and the refund reverses it.
-- name: JournalExchangesFunded :many
SELECT x.id, x.order_id, x.difference_due, x.funded_at::timestamptz AS funded_at, o.currency_code
FROM order_exchanges x
JOIN orders o ON o.id = x.order_id
WHERE x.funded_at >= sqlc.arg('from_at') AND x.funded_at < sqlc.arg('to_at')
  AND (sqlc.narg('currency_code')::text IS NULL OR o.currency_code = sqlc.narg('currency_code')::text)
ORDER BY x.funded_at, x.id
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
WHERE c.id = ANY (sqlc.arg('ids')::text[])
UNION ALL
SELECT x.id, 'exchange'::text, x.order_id, o.currency_code
FROM order_exchanges x
JOIN orders o ON o.id = x.order_id
WHERE x.id = ANY (sqlc.arg('ids')::text[]);
