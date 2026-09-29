-- CreateOrderAddress writes one address of the order.
--
-- It is written INSIDE the order's own transaction, together with the header
-- and the lines: an order that exists without the address it was placed with
-- is an order nobody can ship, and a second statement outside the transaction
-- is a second chance to end up in that state.
-- name: CreateOrderAddress :one
INSERT INTO order_addresses (
    id, order_id, address_type, source_address_id,
    first_name, last_name, company, address_1, address_2,
    city, province, postal_code, country_code, phone, metadata
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7, $8, $9,
    $10, $11, $12, $13, $14, $15
)
RETURNING *;

-- ListOrderAddressesByOrderIDs reads the addresses of several orders in a
-- SINGLE query; there is no query per order (N+1).
--
-- It returns every row, the superseded ones too (ADR 0195): the person's file
-- lists what the order held before a correction, and the timeline dates the
-- corrections by them. Which row is current is the service's reading.
-- name: ListOrderAddressesByOrderIDs :many
SELECT * FROM order_addresses
WHERE order_id = ANY (sqlc.arg('order_ids')::text[])
ORDER BY order_id, address_type, created_at, id;

-- SupersedeOrderAddress closes the order's current address of one type.
--
-- It runs under the order's lock, followed in the same transaction by the
-- corrected row (ADR 0195). It touches the current row only, so a second
-- correction closes the first correction and not the original.
-- The moment is the write's rather than the transaction's (ADR 0241): the
-- correction holds the order's lock, and one that waited for it supersedes a row
-- written after its own transaction began.
-- name: SupersedeOrderAddress :execrows
UPDATE order_addresses
SET superseded_at = clock_timestamp(),
    updated_at    = clock_timestamp()
WHERE order_id = $1
  AND address_type = $2
  AND superseded_at IS NULL;
