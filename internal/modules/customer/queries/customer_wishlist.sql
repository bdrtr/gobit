-- A customer's wishlist (ADR 0190).
--
-- Every write runs under the customer row's lock (GetCustomerForUpdate), so the
-- count and the insert that follows it see the same list.

-- name: InsertWishlistItem :one
INSERT INTO customer_wishlist_item (customer_id, variant_id, created_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetWishlistItem :one
SELECT * FROM customer_wishlist_item
WHERE customer_id = $1 AND variant_id = $2;

-- name: CountWishlistItems :one
SELECT count(*)::bigint AS item_count FROM customer_wishlist_item
WHERE customer_id = $1;

-- ListWishlistItems is not paged: the cap bounds it when it is written.
-- name: ListWishlistItems :many
SELECT * FROM customer_wishlist_item
WHERE customer_id = $1
ORDER BY created_at DESC, variant_id;

-- name: DeleteWishlistItem :execrows
DELETE FROM customer_wishlist_item
WHERE customer_id = $1 AND variant_id = $2;

-- DeleteWishlistOfCustomer is the erasure's: a wishlist row is the person's
-- stated preference and nothing else refers to it.
-- name: DeleteWishlistOfCustomer :execrows
DELETE FROM customer_wishlist_item
WHERE customer_id = $1;

-- name: ListWishlistForDisclosure :many
SELECT * FROM customer_wishlist_item
WHERE customer_id = ANY (sqlc.arg('customer_ids')::text[])
ORDER BY customer_id, created_at, variant_id;
