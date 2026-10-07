-- order_exchanges queries (plan Section 6).
--
-- The record is created, read, listed, WITHDRAWN and -- since ADR 0114 -- can be
-- COMPLETED when it is settled. Both capabilities migration 000008 named have
-- arrived: goods can be shipped against an existing order (ADR 0090), and a
-- difference can be collected into a collection of the exchange's own and
-- recorded on the row (ADR 0120). The completion is still bounded by a CHECK
-- rather than offered for every record, because an exchange whose difference has
-- NOT been collected has had only half of it answered.

-- Every moment an exchange's transition writes is the moment of the write,
-- clock_timestamp(), not the transaction's start (ADR 0241): each transition
-- holds the row's lock, an exchange keeps two moments side by side (funded and
-- completed, or funded and canceled), and the order's history takes the later
-- one as the status. A completion that waited on the funding's lock was stamped
-- before it.

-- name: CreateOrderExchange :one
INSERT INTO order_exchanges (id, order_id, status, difference_due, note, metadata, order_return_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- SetOrderExchangeDifference writes the difference an exchange that names its
-- return derives from what it sends and what it takes back (ADR 0432).
--
-- The narrowing is 'requested' and a named return: a funded exchange holds
-- money for the figure it had, and an exchange written without a return keeps
-- the figure the operator typed. The caller holds the row's lock, so a funding
-- cannot slip in between the read and this write.
-- name: SetOrderExchangeDifference :one
UPDATE order_exchanges
SET difference_due = $2, updated_at = clock_timestamp()
WHERE id = $1 AND status = 'requested' AND order_return_id IS NOT NULL
RETURNING *;

-- LiveExchangesOfReturn names the live exchange that takes a return's goods
-- back, if any (ADR 0432): such a return refunds nothing of its own, is not
-- withdrawn, and no second exchange names it. The unique index
-- order_exchanges_return_uniq holds it to one.
-- name: LiveExchangesOfReturn :many
SELECT id FROM order_exchanges
WHERE order_return_id = $1 AND status <> 'canceled'
ORDER BY id;

-- SumLiveExchangeReplacementTotals is what an exchange sends, priced: the
-- totals of its live replacements' items, and how many of them carry no price
-- (ADR 0432). An exchange that names its return writes no unpriced item, so the
-- second figure is a guard rather than a branch.
-- name: SumLiveExchangeReplacementTotals :one
SELECT COALESCE(SUM(i.total), 0)::bigint AS sent,
       COUNT(*) FILTER (WHERE i.total IS NULL)::bigint AS unpriced
FROM order_replacement_items i
JOIN order_replacements r ON r.id = i.order_replacement_id
WHERE r.order_exchange_id = $1 AND r.status <> 'canceled';

-- SumLiveExchangeLineUnits is how many units of each order line an
-- exchange's live replacements already send (ADR 0432). A line item is priced
-- as the next units of its line: the share of the line's total for the units
-- sent so far with it, less the share for the units sent before it.
-- name: SumLiveExchangeLineUnits :many
SELECT i.order_line_item_id, SUM(i.quantity)::bigint AS units
FROM order_replacement_items i
JOIN order_replacements r ON r.id = i.order_replacement_id
WHERE r.order_exchange_id = $1 AND r.status <> 'canceled'
  AND i.order_line_item_id IS NOT NULL
GROUP BY i.order_line_item_id;

-- SumExchangeOverlapUnits answers, per line, how many units the live exchanges
-- that name their return both take back through it and send again through
-- their live replacements: for each exchange, the fewer of the two. Those are
-- the same goods named twice, so what a line's returns and replacements speak
-- for counts them once (ADR 0432, amending ADR 0423's count).
-- name: SumExchangeOverlapUnits :many
SELECT ri.order_line_item_id,
       SUM(LEAST(ri.quantity, COALESCE(sent.units, 0)))::bigint AS units
FROM order_exchanges e
JOIN order_returns ret ON ret.id = e.order_return_id AND ret.status <> 'canceled'
JOIN order_return_items ri ON ri.order_return_id = ret.id
LEFT JOIN LATERAL (
    SELECT SUM(i.quantity) AS units
    FROM order_replacement_items i
    JOIN order_replacements r ON r.id = i.order_replacement_id
    WHERE r.order_exchange_id = e.id AND r.status <> 'canceled'
      AND i.order_line_item_id = ri.order_line_item_id
) sent ON TRUE
WHERE e.order_return_id IS NOT NULL AND e.status <> 'canceled'
  AND ri.order_line_item_id = ANY (sqlc.arg('line_item_ids')::text[])
GROUP BY ri.order_line_item_id;

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
SET status = 'canceled', canceled_at = clock_timestamp(), updated_at = clock_timestamp()
WHERE id = $1
RETURNING *;

-- CompleteOrderExchange records that the exchange was settled.
--
-- The moment comes from the DATABASE clock for the reason the withdrawal's does.
-- The WHERE narrows to 'requested' rather than trusting a status read a moment
-- ago: the caller holds the row's lock, so this cannot lose a race, and the
-- narrowing is what makes the query safe to read on its own.
--
-- An exchange that owes money it has not collected is refused by
-- order_exchanges_completed_is_settled rather than by this statement. The rule
-- needs only the row, so the database is where it can be kept once instead of in
-- every writer.
-- name: CompleteOrderExchange :one
UPDATE order_exchanges
SET status = 'completed', completed_at = clock_timestamp(), updated_at = clock_timestamp()
WHERE id = $1 AND status IN ('requested', 'funded')
RETURNING *;

-- ReopenOrderExchange takes a completed exchange back to where it stood before
-- its goods left, when their parcel was canceled (ADR 0239): 'funded' when its
-- difference was collected, 'requested' otherwise.
-- name: ReopenOrderExchange :one
UPDATE order_exchanges
SET status = CASE WHEN funded_at IS NOT NULL THEN 'funded' ELSE 'requested' END,
    completed_at = NULL,
    updated_at = clock_timestamp()
WHERE id = $1 AND status = 'completed'
RETURNING *;

-- WithdrawFundedOrderExchange takes back an exchange whose difference was
-- funded and whose money has been sent back.
--
-- It is the EXIT from 'funded' and it goes FORWARD: the record reaches
-- 'canceled', a terminal state, and keeps both moments. funded_at is not
-- cleared -- the exchange really did hold the customer's money and the row goes
-- on saying so, next to the collection that answers for where it went.
--
-- Clearing the moment instead would be the "reopen" ADR 0055 refused, and it
-- would destroy the only local record that money ever moved.
--
-- Whether the money really went back is NOT decided here: it is the payment
-- module's number and only a flow can ask it (ADR 0119). This statement
-- narrows on the status alone.
-- name: WithdrawFundedOrderExchange :one
UPDATE order_exchanges
SET status      = 'canceled',
    canceled_at = clock_timestamp(),
    updated_at  = clock_timestamp()
WHERE id = $1 AND status = 'funded'
RETURNING *;

-- FundOrderExchange names the payment collection that answers this exchange's
-- difference and dates the moment.
--
-- The narrowing is 'requested' alone: funding is written ONCE, and a second
-- call against an already funded exchange matches no row rather than replacing
-- the collection under it. Replacing it would drop the only sentence saying
-- where the first collection's money went.
--
-- What is NOT written here is an amount. The figure lives in the payment
-- module and a copy of it here would be a claim a route this module never
-- hears about can invalidate (ADR 0119); the identifier cannot go stale.
-- name: FundOrderExchange :one
UPDATE order_exchanges
SET status                = 'funded',
    payment_collection_id = $2,
    funded_at             = clock_timestamp(),
    updated_at            = clock_timestamp()
WHERE id = $1 AND status = 'requested'
RETURNING *;

-- ExchangeFundedBy names the exchange a collection funded, if any (ADR 0200):
-- a delivery change refuses a collection an exchange already took.
-- name: ExchangeFundedBy :many
SELECT id FROM order_exchanges
WHERE payment_collection_id = sqlc.arg('payment_collection_id')
LIMIT 1;

-- ListOrderExchangesByIDs reads the given exchanges for the read layer's batch
-- path (ADR 0270), newest first like the order's own listing.
-- name: ListOrderExchangesByIDs :many
SELECT * FROM order_exchanges
WHERE id = ANY (sqlc.arg('ids')::text[])
ORDER BY created_at DESC, id DESC;
