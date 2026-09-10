-- order_replacements queries: what a claim or an exchange promises to send.

-- CreateOrderReplacement writes the promise against ONE source.
--
-- Both source columns are passed and exactly one of them is non-null; the
-- database refuses the other two shapes (order_replacements_one_source). Passing
-- both and letting the CHECK decide is deliberate: a query per source would be
-- two statements to keep in step, and the rule would then live in whichever
-- caller picked between them.
-- name: CreateOrderReplacement :one
INSERT INTO order_replacements
    (id, order_claim_id, order_exchange_id, shipping_option_id, location_id, note)
VALUES ($1, $2, $3, $4, $5, $6)
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

-- ListOrderReplacementsByExchange returns an exchange's replacements, newest
-- first.
-- name: ListOrderReplacementsByExchange :many
SELECT * FROM order_replacements
WHERE order_exchange_id = $1
ORDER BY created_at DESC, id DESC;

-- name: CancelOrderReplacement :one
UPDATE order_replacements
SET status = 'canceled', canceled_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- DispatchOrderReplacement records that the goods left: the moment, the parcel
-- and the status are written together.
--
-- The status and its moment are one write because the schema requires them to
-- agree; the parcel is in the same statement for the same reason, since a
-- dispatched row that named none would violate the CHECK the moment it landed.
-- name: DispatchOrderReplacement :one
UPDATE order_replacements
SET status = 'dispatched',
    dispatched_at = now(),
    fulfillment_id = sqlc.arg('fulfillment_id')::text,
    updated_at = now()
WHERE id = sqlc.arg('id')::text
RETURNING *;
