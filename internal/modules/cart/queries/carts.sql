-- carts queries.
--
-- Every read filters on deleted_at IS NULL (plan Section 8: deletion is soft).
-- The writing queries carry a completed_at IS NULL condition as well: a
-- completed cart is IMMUTABLE, and this is the second gate beside the service's
-- check.

-- name: CreateCart :one
INSERT INTO carts (
    id, region_id, customer_id, email, currency_code, metadata
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetCart :one
SELECT * FROM carts
WHERE id = $1 AND deleted_at IS NULL;

-- LockCart locks the cart for the whole transaction and returns its current
-- state.
--
-- EVERY FLOW THAT CHANGES THE CART STARTS WITH IT. The lock provides two things
-- at once: two concurrent AddLineItem calls cannot corrupt the lines of the cart
-- (the second one sees the line the first wrote and raises the quantity instead
-- of adding a new line), and no other transaction can slip in between the "is it
-- completed" check and the write.
-- name: LockCart :one
SELECT * FROM carts
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: ListCarts :many
SELECT * FROM carts
WHERE deleted_at IS NULL
  AND (sqlc.narg('customer_id')::text IS NULL OR customer_id = sqlc.narg('customer_id')::text)
  AND (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text)
  AND (sqlc.narg('completed')::boolean IS NULL
       OR (completed_at IS NOT NULL) = sqlc.narg('completed')::boolean)
  AND (created_at, id) < (
    COALESCE(sqlc.narg('after_at')::timestamptz, 'infinity'::timestamptz),
    COALESCE(sqlc.narg('after_id')::text, '')
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountCarts gives the total count of the pagination envelope and applies the
-- SAME filters as ListCarts; the two must be changed together.
--
-- The total cannot be read from a window function returned together with the
-- rows: on an out-of-range page no row comes back at all, the window is not
-- evaluated and the total would look like 0. The total is the count of the
-- FILTER, not of the page.
-- name: CountCarts :one
SELECT COUNT(*) FROM carts
WHERE deleted_at IS NULL
  AND (sqlc.narg('customer_id')::text IS NULL OR customer_id = sqlc.narg('customer_id')::text)
  AND (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text)
  AND (sqlc.narg('completed')::boolean IS NULL
       OR (completed_at IS NOT NULL) = sqlc.narg('completed')::boolean);

-- GetCartsByIDs serves the Query layer's FetchByIDs call in ONE round; no per-id
-- query (N+1) is made.
-- name: GetCartsByIDs :many
SELECT * FROM carts
WHERE id = ANY (sqlc.arg('ids')::text[]) AND deleted_at IS NULL
ORDER BY id;

-- UpdateCartContact writes the cart's email and customer as ABSOLUTE values.
--
-- The handover of a guest cart to a registered customer and the email collected
-- at the checkout step both go through this query. The decision of who may be
-- handed over to whom belongs to the SERVICE (replacing a customer that is
-- already set with another one is rejected); the query here only writes.
-- name: UpdateCartContact :one
UPDATE carts
SET email       = $2,
    customer_id = $3,
    updated_at  = now()
WHERE id = $1 AND deleted_at IS NULL AND completed_at IS NULL
RETURNING *;

-- UpdateCartTotals writes the totals the workflow calculated and stamps which
-- shape the totals were calculated for.
-- name: UpdateCartTotals :one
UPDATE carts
SET subtotal        = $2,
    discount_total  = $3,
    tax_total       = $4,
    shipping_total  = $5,
    total           = $6,
    totals_revision = $7,
    updated_at      = now()
WHERE id = $1 AND deleted_at IS NULL AND completed_at IS NULL
RETURNING *;

-- BumpCartRevision raises the cart's shape counter by one; it is called in the
-- SAME transaction after every structural change that affects the totals.
-- name: BumpCartRevision :one
UPDATE carts
SET revision = revision + 1, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL AND completed_at IS NULL
RETURNING *;

-- MarkCartCompleted stamps the cart as completed.
--
-- The completed_at IS NULL condition is deliberate: completing the same cart a
-- second time affects no row and the caller can tell that apart. The service
-- lock already closes the race; the condition here is the second gate, one that
-- covers an intervention made directly through SQL as well.
-- name: MarkCartCompleted :one
UPDATE carts
SET completed_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL AND completed_at IS NULL
RETURNING *;

-- name: SoftDeleteCart :execrows
UPDATE carts
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;
