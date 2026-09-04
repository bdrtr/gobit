-- order_summaries queries.
--
-- A summary is born TOGETHER WITH the order and zeroed out; there is no such
-- thing as "an order without a summary".

-- name: CreateOrderSummary :one
INSERT INTO order_summaries (id, order_id)
VALUES ($1, $2)
RETURNING *;

-- name: GetOrderSummary :one
SELECT * FROM order_summaries
WHERE order_id = $1;

-- SetOrderSummaryTotals merges the CUMULATIVE paid and refunded amounts.
--
-- An incremental write (paid_total = paid_total + $2) was not chosen: payment
-- events are delivered at least once (see core/eventbus) and a repeated event
-- would add the amount TWICE under an incremental write.
--
-- But a plain absolute write (paid_total = $2) is not enough either: at-least-once
-- delivery gives NO ORDERING guarantee. When a delayed capture event is
-- reprocessed, it would write a refund that was recorded later back to zero and
-- nobody would see an error. That is why the write is merged with GREATEST: both
-- amounts are the order's LIFETIME totals and can only GROW. The result is both
-- idempotent (the same value a second time is harmless) and INDEPENDENT OF ORDER
-- — whichever order they arrive in, it converges to the same place and no
-- recorded amount is lost.
--
-- The merge CANNOT BREAK the order_summaries_refund_within_paid constraint: if
-- both inputs satisfy refunded <= paid, then max(r1,r2) <= max(p1,p2) holds too.
-- name: SetOrderSummaryTotals :one
UPDATE order_summaries
SET paid_total     = GREATEST(paid_total, $2),
    refunded_total = GREATEST(refunded_total, $3),
    updated_at     = now()
WHERE order_id = $1
RETURNING *;
