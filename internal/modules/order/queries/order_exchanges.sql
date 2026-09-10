-- order_exchanges queries (plan Section 6).
--
-- The record is created, read, listed, WITHDRAWN and -- since ADR 0114 -- can be
-- COMPLETED when it owes nothing. The completion came back because one of the two
-- capabilities migration 000008 named arrived: goods can now be shipped against an
-- existing order (ADR 0090), and an exchange whose difference_due is zero needs
-- nothing else. The other half is still missing -- the order-to-payment link is
-- one-to-one, so money cannot be collected against an existing order -- which is
-- why the completion is bounded by a CHECK rather than offered for every record.

-- name: CreateOrderExchange :one
INSERT INTO order_exchanges (id, order_id, status, difference_due, note, metadata)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetOrderExchange :one
SELECT * FROM order_exchanges
WHERE id = $1;

-- name: ListOrderExchanges :many
SELECT * FROM order_exchanges
WHERE order_id = $1
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- name: CountOrderExchanges :one
SELECT COUNT(*) FROM order_exchanges
WHERE order_id = $1;

-- LockOrderExchange locks the exchange row until the end of the transaction.
--
-- The reason is the one LockOrderReturn states: the transition reads the
-- current status and writes the next one, and two operators withdrawing the
-- same exchange at the same moment would otherwise both read "requested" and
-- both write a timestamp, leaving the record holding the later one.
-- name: LockOrderExchange :one
SELECT * FROM order_exchanges
WHERE id = $1
FOR UPDATE;

-- CancelOrderExchange withdraws the exchange request.
--
-- canceled_at comes from the DATABASE clock, as every other after-sales stamp
-- does: the moment belongs to the record, and letting the caller supply it
-- makes the ordering of two records depend on which machine wrote them.
-- name: CancelOrderExchange :one
UPDATE order_exchanges
SET status = 'canceled', canceled_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- CompleteOrderExchange records that the exchange was settled.
--
-- The moment comes from the DATABASE clock for the reason the withdrawal's does.
-- The WHERE narrows to 'requested' rather than trusting a status read a moment
-- ago: the caller holds the row's lock, so this cannot lose a race, and the
-- narrowing is what makes the query safe to read on its own.
--
-- An exchange that owes money is refused by order_exchanges_completed_owes_nothing
-- rather than by this statement. The rule needs only the row, so the database is
-- where it can be kept once instead of in every writer.
-- name: CompleteOrderExchange :one
UPDATE order_exchanges
SET status = 'completed', completed_at = now(), updated_at = now()
WHERE id = $1 AND status = 'requested'
RETURNING *;
