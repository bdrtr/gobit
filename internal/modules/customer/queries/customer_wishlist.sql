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

-- A wishlist item's stock alert (ADR 0215).

-- MarkStockAlert marks an item and forgets any earlier arming: a mark set again
-- waits again to see the variant run out.
-- name: MarkStockAlert :one
UPDATE customer_wishlist_item
SET stock_alert = true,
    stock_alert_channels = sqlc.narg('channels')::text[],
    stock_alert_armed_at = NULL
WHERE customer_id = sqlc.arg('customer_id') AND variant_id = sqlc.arg('variant_id')
RETURNING *;

-- name: UnmarkStockAlert :execrows
UPDATE customer_wishlist_item
SET stock_alert = false, stock_alert_channels = NULL, stock_alert_armed_at = NULL
WHERE customer_id = $1 AND variant_id = $2 AND stock_alert;

-- ListAlerts pages the items of live customers marked for their stock or their
-- price, in key order (ADR 0215, ADR 0216).
-- name: ListAlerts :many
SELECT w.* FROM customer_wishlist_item w
JOIN customer c ON c.id = w.customer_id
WHERE (w.stock_alert OR w.price_alert) AND c.deleted_at IS NULL
  AND (w.customer_id, w.variant_id) > (sqlc.arg('after_customer_id')::text, sqlc.arg('after_variant_id')::text)
ORDER BY w.customer_id, w.variant_id
LIMIT sqlc.arg('row_limit');

-- ArmStockAlert records that a marked variant was seen out of stock.
-- name: ArmStockAlert :execrows
UPDATE customer_wishlist_item
SET stock_alert_armed_at = now()
WHERE customer_id = $1 AND variant_id = $2 AND stock_alert AND stock_alert_armed_at IS NULL;

-- ClearStockAlert clears a mark once its mail went, only if it is still the
-- arming the mail was sent for.
-- name: ClearStockAlert :execrows
UPDATE customer_wishlist_item
SET stock_alert = false, stock_alert_channels = NULL, stock_alert_armed_at = NULL
WHERE customer_id = $1 AND variant_id = $2 AND stock_alert_armed_at = $3;

-- A wishlist item's price alert (ADR 0216).

-- MarkPriceAlert marks an item with the region and channels its price is asked
-- in; the baseline is left for the alert job to record.
-- name: MarkPriceAlert :one
UPDATE customer_wishlist_item
SET price_alert = true,
    price_alert_marked_at = sqlc.arg('marked_at'),
    price_alert_region_id = sqlc.arg('region_id'),
    price_alert_channels = sqlc.narg('channels')::text[],
    price_alert_currency = NULL,
    price_alert_amount = NULL
WHERE customer_id = sqlc.arg('customer_id') AND variant_id = sqlc.arg('variant_id')
RETURNING *;

-- name: UnmarkPriceAlert :execrows
UPDATE customer_wishlist_item
SET price_alert = false, price_alert_marked_at = NULL, price_alert_region_id = NULL,
    price_alert_channels = NULL, price_alert_currency = NULL, price_alert_amount = NULL
WHERE customer_id = $1 AND variant_id = $2 AND price_alert;

-- RecordPriceBaseline records the price at the mark, once, for the mark it was
-- read for.
-- name: RecordPriceBaseline :execrows
UPDATE customer_wishlist_item
SET price_alert_currency = sqlc.arg('currency'), price_alert_amount = sqlc.arg('amount')
WHERE customer_id = sqlc.arg('customer_id') AND variant_id = sqlc.arg('variant_id')
  AND price_alert AND price_alert_marked_at = sqlc.arg('marked_at') AND price_alert_amount IS NULL;

-- ClearPriceAlert clears a mark once its mail went, only while it is the mark
-- the mail was sent for.
-- name: ClearPriceAlert :execrows
UPDATE customer_wishlist_item
SET price_alert = false, price_alert_marked_at = NULL, price_alert_region_id = NULL,
    price_alert_channels = NULL, price_alert_currency = NULL, price_alert_amount = NULL
WHERE customer_id = $1 AND variant_id = $2 AND price_alert_marked_at = $3;
