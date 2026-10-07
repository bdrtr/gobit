-- order_replacement_items queries: which lines are being replaced.

-- Exactly ONE of order_line_item_id and variant_id is set; the CHECK in
-- migration 000019 holds it, and the service decides which shape a request has.
-- An item of an exchange that names its return carries its price (ADR 0432);
-- every other item leaves the six price columns NULL together.
-- name: CreateOrderReplacementItem :one
INSERT INTO order_replacement_items
    (id, order_replacement_id, order_line_item_id, variant_id, quantity,
     unit_price, total, tax_total, tax_rate_bps, tax_components, priced_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- ListOrderReplacementItems returns a replacement's lines in the order they
-- were written: they share a created_at, and seq is the order the database
-- took them in (D161). The dispatch sets their units aside in this order.
-- name: ListOrderReplacementItems :many
SELECT * FROM order_replacement_items
WHERE order_replacement_id = $1
ORDER BY created_at, seq;

-- ListOrderReplacementItemsOfReplacements reads the lines of the given
-- replacements in one query, each replacement's in the order they were
-- written, for the read layer (ADR 0270).
-- name: ListOrderReplacementItemsOfReplacements :many
SELECT * FROM order_replacement_items
WHERE order_replacement_id = ANY (sqlc.arg('replacement_ids')::text[])
ORDER BY order_replacement_id, created_at, seq;

-- SumReplacedQuantities reports how many units of each of the given order lines
-- have ALREADY been promised, across every live replacement of the order.
--
-- # Why canceled replacements are excluded
--
-- A withdrawn promise releases the units it was holding: they can be promised
-- again. An open one must count, or two requests could each promise the whole
-- line and together send twice what was bought.
--
-- The service reads this under the order's lock and compares it against the
-- ordered quantity, exactly as SumReturnedQuantities is used. It has to be a
-- query rather than a CHECK because the rule spans rows.
--
-- # Variant-shaped rows are NOT counted, and cannot be
--
-- Since migration 000019 a replacement item may name a VARIANT instead of a line
-- (ADR 0145). The ceiling this sum feeds is "no more of a line than was bought on
-- it", and a row that names no line is not against any line's ceiling — it is
-- goods the order never sold. The `IS NOT NULL` is therefore a statement rather
-- than a filter: counting such a row would attribute it to a line chosen by
-- nothing.
--
-- What stands in for a ceiling on a variant-shaped row is the exchange's
-- difference, and it bounds less than this sum does: a dispatch is refused only
-- when a collection the exchange was funded with no longer holds it (ADR 0124).
-- name: SumReplacedQuantities :many
SELECT i.order_line_item_id, SUM(i.quantity)::bigint AS replaced
FROM order_replacement_items i
JOIN order_replacements r ON r.id = i.order_replacement_id
WHERE i.order_line_item_id = ANY(sqlc.arg('line_item_ids')::text[])
  AND i.order_line_item_id IS NOT NULL
  AND r.status <> 'canceled'
GROUP BY i.order_line_item_id;

-- SetOrderReplacementItemReservation writes the promise a line's units are held
-- under.
--
-- It is written BEFORE the units are confirmed, so a dispatch that dies between
-- the two finds the promise on the row instead of making a second one.
-- name: SetOrderReplacementItemReservation :one
UPDATE order_replacement_items
SET reservation_id = sqlc.arg('reservation_id')::text,
    updated_at = now()
WHERE id = sqlc.arg('id')::text
RETURNING *;

-- ClearOrderReplacementItemReservations forgets the promises a recalled
-- replacement's lines held (ADR 0239): their units are back on the shelf, and
-- the next dispatch makes new ones.
-- name: ClearOrderReplacementItemReservations :exec
UPDATE order_replacement_items
SET reservation_id = NULL, updated_at = now()
WHERE order_replacement_id = $1 AND reservation_id IS NOT NULL;

-- ListLiveExchangeLineItems returns the items naming an order line that an
-- exchange's live replacements send, in the order they were written: each
-- item of a line is priced as the units after the ones before it (ADR 0432),
-- and a withdrawal prices the rest again from the first unit. The caller holds
-- the exchange's lock, which every writer of its replacements takes, so seq is
-- the order of the writes.
-- name: ListLiveExchangeLineItems :many
SELECT i.id, i.order_line_item_id, i.quantity, i.total, i.tax_total, i.tax_components
FROM order_replacement_items i
JOIN order_replacements r ON r.id = i.order_replacement_id
WHERE r.order_exchange_id = $1 AND r.status <> 'canceled'
  AND i.order_line_item_id IS NOT NULL AND i.priced_by = 'line'
ORDER BY i.seq;

-- RepriceOrderReplacementItem writes a line item's figures again when a
-- withdrawal before it moved the units it sends (ADR 0432); only an item a
-- line priced is written, and its unit price and source stay.
-- name: RepriceOrderReplacementItem :execrows
UPDATE order_replacement_items
SET total = sqlc.arg('total')::bigint,
    tax_total = sqlc.arg('tax_total')::bigint,
    tax_components = sqlc.narg('tax_components')::jsonb,
    updated_at = now()
WHERE id = sqlc.arg('id')::text AND priced_by = 'line';
