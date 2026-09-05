-- order_exchanges queries (plan Section 6).
--
-- The record is created, read, listed and WITHDRAWN. It is never completed, and
-- that is a capability statement rather than an omission: completing an
-- exchange means shipping goods against an existing order and — when
-- difference_due is positive — collecting money against one, and the framework
-- can do neither today (migration 000008 carries the argument and the sources).
-- The column that used to promise it is gone, so there is no stamp here left
-- without a writer.

-- name: CreateOrderExchange :one
INSERT INTO order_exchanges (id, order_id, status, difference_due, note, metadata)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetOrderExchange :one
SELECT * FROM order_exchanges
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListOrderExchanges :many
SELECT * FROM order_exchanges
WHERE order_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- name: CountOrderExchanges :one
SELECT COUNT(*) FROM order_exchanges
WHERE order_id = $1 AND deleted_at IS NULL;

-- LockOrderExchange locks the exchange row until the end of the transaction.
--
-- The reason is the one LockOrderReturn states: the transition reads the
-- current status and writes the next one, and two operators withdrawing the
-- same exchange at the same moment would otherwise both read "requested" and
-- both write a timestamp, leaving the record holding the later one.
-- name: LockOrderExchange :one
SELECT * FROM order_exchanges
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- CancelOrderExchange withdraws the exchange request.
--
-- canceled_at comes from the DATABASE clock, as every other after-sales stamp
-- does: the moment belongs to the record, and letting the caller supply it
-- makes the ordering of two records depend on which machine wrote them.
-- name: CancelOrderExchange :one
UPDATE order_exchanges
SET status = 'canceled', canceled_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;
