-- cart_shipping_methods queries.

-- name: CreateShippingMethod :one
INSERT INTO cart_shipping_methods (
    id, cart_id, name, shipping_option_id, amount, data
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetShippingMethod :one
SELECT * FROM cart_shipping_methods
WHERE id = $1 AND cart_id = $2 AND deleted_at IS NULL;

-- name: ListShippingMethods :many
SELECT * FROM cart_shipping_methods
WHERE cart_id = $1 AND deleted_at IS NULL
ORDER BY created_at, id;

-- ListShippingMethodsOfCarts is ListShippingMethods for many carts in one
-- read; the query provider's shipping_methods field reads it (ADR 0291).
-- name: ListShippingMethodsOfCarts :many
SELECT * FROM cart_shipping_methods
WHERE cart_id = ANY(sqlc.arg(cart_ids)::text[]) AND deleted_at IS NULL
ORDER BY cart_id, created_at, id;

-- name: SoftDeleteShippingMethod :execrows
UPDATE cart_shipping_methods
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND cart_id = $2 AND deleted_at IS NULL;

-- name: SoftDeleteShippingMethodsByCart :exec
UPDATE cart_shipping_methods
SET deleted_at = now(), updated_at = now()
WHERE cart_id = $1 AND deleted_at IS NULL;
