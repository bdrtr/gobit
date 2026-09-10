-- name: AddCartPromotionCode :exec
-- The same code typed twice is a double press and not a second coupon, so the
-- conflict is absorbed here rather than turned into an error the storefront has
-- to explain.
INSERT INTO cart_promotion_code (cart_id, code)
VALUES ($1, $2)
ON CONFLICT (cart_id, code) DO NOTHING;

-- name: ListCartPromotionCodes :many
SELECT code FROM cart_promotion_code
WHERE cart_id = $1
ORDER BY created_at, code;

-- name: ListCartPromotionCodesByCarts :many
-- The batched read: the codes of several carts in ONE query, so a listing that
-- carries them does not pay a query per cart.
SELECT cart_id, code FROM cart_promotion_code
WHERE cart_id = ANY($1::text[])
ORDER BY cart_id, created_at, code;

-- name: DeleteCartPromotionCode :execrows
DELETE FROM cart_promotion_code
WHERE cart_id = $1 AND code = $2;

-- name: DeleteCartPromotionCodesByCart :exec
DELETE FROM cart_promotion_code WHERE cart_id = $1;

