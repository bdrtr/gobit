-- order_credit_lines queries.
--
-- A credit line is written once and never updated: it is a record of a
-- concession that was made, and a concession that is edited afterwards is not
-- the same record. Withdrawing one is a second line in the other direction --
-- which this table cannot hold, deliberately (see migration 000014).

-- name: CreateOrderCreditLine :one
INSERT INTO order_credit_lines (id, order_id, amount, reason, note)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListOrderCreditLines :many
SELECT * FROM order_credit_lines
WHERE order_id = $1
ORDER BY created_at, id;

-- SumOrderCreditLines is the running total, read rather than stored.
--
-- COALESCE because an order with no credit lines has to answer 0 rather than
-- NULL: the caller subtracts this from the order's total and a NULL would make
-- the whole outstanding amount NULL.

-- name: SumOrderCreditLines :one
SELECT COALESCE(sum(amount), 0)::bigint FROM order_credit_lines
WHERE order_id = $1;
