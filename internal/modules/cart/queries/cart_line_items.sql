-- cart_line_items queries.
--
-- Lines are always changed under the CART LOCK (see LockCart); that is why the
-- line itself is not locked separately. The lock order is single: the cart
-- first, then the line. A lock order that varies with the flow means a deadlock.

-- name: CreateLineItem :one
INSERT INTO cart_line_items (
    id, cart_id, variant_id, title, quantity, unit_price, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetLineItem :one
SELECT * FROM cart_line_items
WHERE id = $1 AND cart_id = $2 AND deleted_at IS NULL;

-- GetLineItemByVariant returns the LIVING line of a variant in the cart.
--
-- AddLineItem uses it: when the same variant is added a second time it raises
-- the quantity of the existing line instead of opening a new one (see
-- service.AddLineItem).
-- name: GetLineItemByVariant :one
SELECT * FROM cart_line_items
WHERE cart_id = $1 AND variant_id = $2 AND deleted_at IS NULL;

-- name: ListLineItems :many
SELECT * FROM cart_line_items
WHERE cart_id = $1 AND deleted_at IS NULL
ORDER BY created_at, id;

-- ListLineItemsByCartIDs returns the lines of several carts in ONE query; no
-- per-cart query (N+1) is made.
-- name: ListLineItemsByCartIDs :many
SELECT * FROM cart_line_items
WHERE cart_id = ANY (sqlc.arg('cart_ids')::text[]) AND deleted_at IS NULL
ORDER BY cart_id, created_at, id;

-- name: SetLineItemQuantity :one
UPDATE cart_line_items
SET quantity = $3, updated_at = now()
WHERE id = $1 AND cart_id = $2 AND deleted_at IS NULL
RETURNING *;

-- SetLineItemTotals writes ALL the line amounts of one calculation round in a
-- SINGLE statement.
--
-- The quantity does NOT change HERE: the quantity is the cart service's data,
-- the amounts are the workflow's. Keeping the two in separate queries makes it
-- structurally impossible for a calculation round to change the quantity
-- silently.
--
-- The statement is SINGLE because one UPDATE per line used to run under the
-- cart's LOCK. Measured (local container, TCP round trip ~30 µs, cart of 100
-- lines, the time from taking the lock until the LAST WRITE returns, p50): one
-- UPDATE per line 8.0 ms, the same UPDATEs in a single pipeline 3.0 ms, the
-- single statement here 0.55 ms. Only a part of the gain comes from the round
-- trips; the rest is the per-statement parse/plan cost, and only a SINGLE
-- statement erases that — a pipeline (pgx batch, sqlc :batchexec) would have
-- stopped at two thirds of the gain.
--
-- WHAT THE MEASUREMENT DOES NOT INCLUDE: the harness's container runs with
-- fsync=off (the testcontainers-go postgres module hard-codes it), so the times
-- above are the WRITE PHASE under the lock, NOT the WAL flush of the commit that
-- follows. The flush is under the same lock too — the lock is released at
-- commit — and this change does NOT TOUCH it: measured on a durable cluster
-- (fsync=on, same machine), a commit updating 1 line and a commit updating 100
-- lines both take 6.2 ms. So the real lock time drops from ~14.2 ms to ~6.8 ms:
-- the gain is not 14x but ~2x. The 14x is the ratio inside the write phase
-- itself, and that is what the claim about the number of statements is about.
--
-- The matching is done by the ORDER of the arrays: the amounts at the same index
-- as v.id go to the same line. The arrays are built in a single loop (see the
-- repository), so their lengths are structurally equal; were they not, ROWS FROM
-- would pad the short array with NULL and the NOT NULL constraint would drop the
-- statement — a noisy error, not a silently wrong amount.
--
-- ROWS FROM (unnest(a), unnest(b), ...) is the same thing as the multi-argument
-- unnest(a, b, ...); this form was written because sqlc cannot parse the
-- multi-argument one. Measured: there is no difference between the two (100
-- lines, p50 545 µs / 555 µs).
--
-- cart_id is part of the WHERE, not of the JOIN: the line of another cart can
-- match in no array. An id that does not match does not show up in RETURNING,
-- and the caller compares the written ids with the requested ones and drops the
-- round.
--
-- No repeated id is passed (the service rejects it): UPDATE ... FROM does not
-- define WHICH source wins when the same target row matches more than one source
-- row.
-- name: SetLineItemTotals :many
UPDATE cart_line_items AS li
SET unit_price     = v.unit_price,
    subtotal       = v.subtotal,
    discount_total = v.discount_total,
    tax_total      = v.tax_total,
    total          = v.total,
    updated_at     = now()
FROM ROWS FROM (
    unnest(sqlc.arg('line_ids')::text[]),
    unnest(sqlc.arg('unit_prices')::bigint[]),
    unnest(sqlc.arg('subtotals')::bigint[]),
    unnest(sqlc.arg('discount_totals')::bigint[]),
    unnest(sqlc.arg('tax_totals')::bigint[]),
    unnest(sqlc.arg('totals')::bigint[])
) AS v (id, unit_price, subtotal, discount_total, tax_total, total)
WHERE li.id = v.id
  AND li.cart_id = sqlc.arg('cart_id')
  AND li.deleted_at IS NULL
RETURNING li.id;

-- name: SoftDeleteLineItem :execrows
UPDATE cart_line_items
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND cart_id = $2 AND deleted_at IS NULL;

-- name: SoftDeleteLineItemsByCart :exec
UPDATE cart_line_items
SET deleted_at = now(), updated_at = now()
WHERE cart_id = $1 AND deleted_at IS NULL;
