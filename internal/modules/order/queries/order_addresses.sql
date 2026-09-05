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
-- name: ListOrderAddressesByOrderIDs :many
SELECT * FROM order_addresses
WHERE order_id = ANY (sqlc.arg('order_ids')::text[])
ORDER BY order_id, address_type;
