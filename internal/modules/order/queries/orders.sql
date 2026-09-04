-- orders queries.
--
-- Every read filters on deleted_at IS NULL (plan Section 8: deletion is soft).
-- State-changing queries additionally require the EXPECTED STATE; this is the
-- second gate next to the service's check under the lock, and it covers an
-- intervention made directly through SQL as well.

-- CreateOrder writes a new order.
--
-- The display_id column is DELIBERATELY absent from the list: an explicit value
-- cannot be written to a GENERATED ALWAYS AS IDENTITY column, and the sequence
-- produces the number. That is why it is impossible for two concurrent INSERTs
-- to get the same number.
-- name: CreateOrder :one
INSERT INTO orders (
    id, status, region_id, customer_id, email, currency_code,
    cart_id, idempotency_key,
    subtotal, discount_total, tax_total, shipping_total, total,
    metadata, placed_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8,
    $9, $10, $11, $12, $13,
    $14, now()
)
RETURNING *;

-- name: GetOrder :one
SELECT * FROM orders
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetOrderByDisplayID :one
SELECT * FROM orders
WHERE display_id = $1 AND deleted_at IS NULL;

-- GetOrderByIdempotencyKey returns the order opened with the same key.
--
-- A retried saga step finds the existing order here and does not open a second
-- order (Principle 2.6).
-- name: GetOrderByIdempotencyKey :one
SELECT * FROM orders
WHERE idempotency_key = $1 AND deleted_at IS NULL;

-- LockOrder locks the order for the duration of the transaction and returns its
-- current form.
--
-- EVERY STATE-CHANGING FLOW STARTS WITH THIS. The lock prevents another
-- transaction from stepping in between "read the state" and "write the state":
-- otherwise a concurrent CancelOrder and CompleteOrder would both see the order
-- as 'pending' and both would set out to write.
-- name: LockOrder :one
SELECT * FROM orders
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: ListOrders :many
SELECT * FROM orders
WHERE deleted_at IS NULL
  AND (sqlc.narg('customer_id')::text IS NULL OR customer_id = sqlc.narg('customer_id')::text)
  AND (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountOrders gives the total count of the pagination envelope and applies the
-- SAME filters as ListOrders; the two must be changed together.
--
-- The total cannot be read from a window function returned along with the rows:
-- on an out-of-range page no rows come back at all, the window is not evaluated
-- and the total would appear as 0. The total is the count of the FILTER, not of
-- the page.
-- name: CountOrders :one
SELECT COUNT(*) FROM orders
WHERE deleted_at IS NULL
  AND (sqlc.narg('customer_id')::text IS NULL OR customer_id = sqlc.narg('customer_id')::text)
  AND (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text);

-- GetOrdersByIDs satisfies the Query layer's FetchByIDs call in a SINGLE round
-- trip; no per-ID query (N+1) is made.
-- name: GetOrdersByIDs :many
SELECT * FROM orders
WHERE id = ANY (sqlc.arg('ids')::text[]) AND deleted_at IS NULL
ORDER BY id;

-- CancelOrder cancels the order and stamps the moment of cancellation.
--
-- The status = 'pending' condition is deliberate: a completed or archived order
-- CANNOT be canceled from here. On an already canceled order no row is affected
-- either; the caller tells this apart and behaves idempotently
-- (see service.Service.CancelOrder).
-- name: CancelOrder :one
UPDATE orders
SET status        = 'canceled',
    canceled_at   = now(),
    cancel_reason = $2,
    updated_at    = now()
WHERE id = $1 AND deleted_at IS NULL AND status = 'pending'
RETURNING *;

-- CompleteOrder stamps the order as completed.
-- name: CompleteOrder :one
UPDATE orders
SET status       = 'completed',
    completed_at = now(),
    updated_at   = now()
WHERE id = $1 AND deleted_at IS NULL AND status = 'pending'
RETURNING *;

-- ArchiveOrder takes a completed order into the archive.
--
-- completed_at IS NOT TOUCHED: archiving does not change the order's moment of
-- completion, it only moves it out of the day-to-day lists.
-- name: ArchiveOrder :one
UPDATE orders
SET status     = 'archived',
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL AND status = 'completed'
RETURNING *;
