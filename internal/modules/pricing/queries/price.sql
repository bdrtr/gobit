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

-- SoftDeletePricesBySet hides a set's prices behind deleted_at.
--
-- Since ADR 0047 it has exactly ONE caller, DeletePriceSet, and that is what
-- gives price.deleted_at a single meaning: a stamped price row can only have
-- come from a deleted set. The stamp cannot be replaced by DeletePricesBySet
-- here, because it is the only thing that hides a deleted set's prices from a
-- calculation: ListPriceCandidates does not join price_set at all, and the
-- service reads the set only when zero candidates came back, which is what
-- keeps the happy path to one round trip.
-- name: SoftDeletePricesBySet :exec
UPDATE price
SET deleted_at = $2, updated_at = $2
WHERE price_set_id = $1 AND deleted_at IS NULL;

-- DeletePricesBySet removes a set's live prices for good, so that a replaced
-- price leaves nothing behind (ADR 0047).
--
-- What the soft-delete stamp used to leave behind was not a history and could
-- not be made into one by reading it: successive generations of one price share
-- no id because a replace mints a new one, no column says WHY a row was retired
-- so a superseded price and a deleted set's price are byte-identical, and
-- neither of the table's indexes contains a retired row because both are
-- partial on deleted_at IS NULL. What a customer PAID is recorded by the cart
-- and the order line that charged them; what these rows held was the price on a
-- day nobody bought.
--
-- The liveness predicate is NOT ceremony inherited from the stamp. A partial
-- index can only serve a statement whose own predicate implies the index's, and
-- price_set_id_idx is partial on exactly this one: measured, the predicated
-- delete plans as an index scan over that index and the same delete without the
-- predicate plans as a sequential scan of the whole table. A price edit is an
-- operator write on a table every storefront price calculation also reads, so
-- paying a full scan per edit to tidy rows nobody reads is the wrong trade.
-- What the predicate costs is that rows an earlier version of this code already
-- stamped are invisible here and stay; clearing those is an operator's one-off
-- cleanup, not a hidden step at boot.
--
-- A price's rules go with it through the ON DELETE CASCADE on
-- price_rule.price_id rather than through a second statement. A stamp never
-- fired that cascade, so every superseded generation used to leave its rules
-- standing live behind a parent no read could reach.
-- name: DeletePricesBySet :exec
DELETE FROM price
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
