-- price_set queries. Every read applies the deleted_at IS NULL filter.

-- name: InsertPriceSet :one
INSERT INTO price_set (id, created_at, updated_at)
VALUES ($1, $2, $2)
RETURNING *;

-- name: GetPriceSet :one
SELECT * FROM price_set
WHERE id = $1 AND deleted_at IS NULL;

-- GetPriceSetForUpdate reads the set and locks its row UNTIL THE END OF THE
-- TRANSACTION.
--
-- An existence check without the lock does not preserve replace semantics: of
-- two concurrent writes, the second one's "delete the old prices" step cannot
-- see the first one's NEW rows in its own statement snapshot under READ
-- COMMITTED, and so does not delete them; the result is that both writes'
-- prices stay live in the set TOGETHER. The row lock serializes the writes made
-- to one and the same set.
--
-- FOR UPDATE RE-EVALUATES the WHERE clause after the lock is taken; a delete
-- that slipped in between therefore surfaces as "no rows", and prices do not
-- stick to a set that has been deleted.
-- name: GetPriceSetForUpdate :one
SELECT * FROM price_set
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: ListPriceSets :many
SELECT * FROM price_set
WHERE deleted_at IS NULL
ORDER BY id
LIMIT $1 OFFSET $2;

-- name: CountPriceSets :one
SELECT count(*) FROM price_set
WHERE deleted_at IS NULL;

-- name: GetPriceSetsByIDs :many
SELECT * FROM price_set
WHERE id = ANY(@ids::text[]) AND deleted_at IS NULL
ORDER BY id;

-- name: SoftDeletePriceSet :one
UPDATE price_set
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;
