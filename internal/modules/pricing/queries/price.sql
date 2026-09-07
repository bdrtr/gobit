-- name: InsertPrice :one
INSERT INTO price (
    id, price_set_id, price_list_id, currency_code,
    amount, min_quantity, max_quantity, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING *;

-- name: GetPrice :one
SELECT * FROM price
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListPricesBySet :many
SELECT * FROM price
WHERE price_set_id = $1 AND deleted_at IS NULL
ORDER BY id;

-- name: SoftDeletePricesBySet :exec
UPDATE price
SET deleted_at = $2, updated_at = $2
WHERE price_set_id = $1 AND deleted_at IS NULL;

-- ListPriceCandidates returns ALL of a price set's prices together with the
-- metadata of the price list each one hangs from.
--
-- Currency, quantity range and list validity are NOT filtered out here: every
-- branch of the selection rule lives in the pure function in the service layer,
-- where it can be proven by a unit test with no database. The LEFT JOIN's
-- deleted_at condition is written on the JOIN and not on the table; a price
-- attached to a deleted list therefore keeps its row, but its list metadata
-- comes back NULL and the service can discard it.
-- name: ListPriceCandidates :many
SELECT
    p.*,
    pl.id        AS list_id,
    pl.type      AS list_type,
    pl.status    AS list_status,
    pl.starts_at AS list_starts_at,
    pl.ends_at   AS list_ends_at
FROM price p
LEFT JOIN price_list pl
       ON pl.id = p.price_list_id AND pl.deleted_at IS NULL
WHERE p.price_set_id = $1 AND p.deleted_at IS NULL
ORDER BY p.id;

-- ListPriceCandidatesBySets returns the same rows for MORE THAN ONE set in a
-- single round trip.
--
-- It is batched for the Query layer's ban on N+1 (ADR 0004). It returns the
-- same columns as the singular version so that the read surface and the
-- calculation see the SAME input: if the list metadata were not carried, the
-- provider could not tell the price of an unpublished campaign apart from the
-- base price.
-- name: ListPriceCandidatesBySets :many
SELECT
    p.*,
    pl.id        AS list_id,
    pl.type      AS list_type,
    pl.status    AS list_status,
    pl.starts_at AS list_starts_at,
    pl.ends_at   AS list_ends_at
FROM price p
LEFT JOIN price_list pl
       ON pl.id = p.price_list_id AND pl.deleted_at IS NULL
WHERE p.price_set_id = ANY(@price_set_ids::text[]) AND p.deleted_at IS NULL
ORDER BY p.price_set_id, p.id;
