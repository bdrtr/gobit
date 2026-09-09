-- order_replacements queries: what a claim promises to send.

-- name: CreateOrderReplacement :one
INSERT INTO order_replacements
    (id, order_claim_id, shipping_option_id, location_id, note)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetOrderReplacement :one
SELECT * FROM order_replacements
WHERE id = $1;

-- GetOrderReplacementForUpdate takes the row's lock.
--
-- The transition reads the current status and writes the next one; without the
-- lock two withdrawals could each read 'requested' and both write a moment,
-- and the second would overwrite the first's.
-- name: GetOrderReplacementForUpdate :one
SELECT * FROM order_replacements
WHERE id = $1
FOR UPDATE;

-- ListOrderReplacementsByClaim returns a claim's replacements, newest first.
-- name: ListOrderReplacementsByClaim :many
SELECT * FROM order_replacements
WHERE order_claim_id = $1
ORDER BY created_at DESC, id DESC;

-- name: CancelOrderReplacement :one
UPDATE order_replacements
SET status = 'canceled', canceled_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;
