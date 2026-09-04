-- shipping_options queries.
--
-- The read path of the option catalog splits in two:
--
--   - Admin listing (ListShippingOptions) — paginated and filtered.
--   - Eligibility listing (ListEligibleShippingOptions) — returns the
--     CANDIDATES for a cart context. Rule matching is NOT DONE here; only the
--     eliminations that are cheap at the column level (region, currency,
--     profile, return, admin_only) are handed to SQL. The rule itself lives in
--     the pure function in the service layer and can be proven by a unit test
--     without a database.

-- name: CreateShippingOption :one
INSERT INTO shipping_options (
    id, name, provider_id, shipping_profile_id, price_type, amount,
    currency_code, region_id, is_return, admin_only, data, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: GetShippingOption :one
SELECT * FROM shipping_options
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListShippingOptions :many
SELECT * FROM shipping_options
WHERE deleted_at IS NULL
  AND (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text)
  AND (sqlc.narg('profile_id')::text IS NULL OR shipping_profile_id = sqlc.narg('profile_id')::text)
  AND (sqlc.narg('provider_id')::text IS NULL OR provider_id = sqlc.narg('provider_id')::text)
  AND (sqlc.narg('price_type')::text IS NULL OR price_type = sqlc.narg('price_type')::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountShippingOptions applies the SAME filters as ListShippingOptions; for the
-- rationale see CountShippingProfiles.
-- name: CountShippingOptions :one
SELECT COUNT(*) FROM shipping_options
WHERE deleted_at IS NULL
  AND (sqlc.narg('region_id')::text IS NULL OR region_id = sqlc.narg('region_id')::text)
  AND (sqlc.narg('profile_id')::text IS NULL OR shipping_profile_id = sqlc.narg('profile_id')::text)
  AND (sqlc.narg('provider_id')::text IS NULL OR provider_id = sqlc.narg('provider_id')::text)
  AND (sqlc.narg('price_type')::text IS NULL OR price_type = sqlc.narg('price_type')::text);

-- GetShippingOptionsByIDs serves the Query layer's FetchByIDs call in a SINGLE
-- round trip; no query per id (N+1) is made.
-- name: GetShippingOptionsByIDs :many
SELECT * FROM shipping_options
WHERE id = ANY (sqlc.arg('ids')::text[]) AND deleted_at IS NULL
ORDER BY id;

-- ListEligibleShippingOptions returns the CANDIDATES of a cart context.
--
-- The region_id filter accepts TWO values: the option's region is either the
-- requested region or the EMPTY string. Empty means "every region", and it
-- keeps an option of a store that has no region (or an option that is
-- independent of the region) from dropping off the list.
--
-- If profile_ids is given as an empty array the profile filter is NOT APPLIED:
-- if the cart's products are bound to no profile at all, every profile becomes
-- a candidate. Otherwise an empty cart could see no shipping option at all.
--
-- If include_admin_only is false, admin_only options are ELIMINATED. The filter
-- stays in SQL, because this is the only field that must not leak to the store
-- surface, and not reading the row at all is safer than reading it and then
-- throwing it away.
--
-- An option WHOSE PROFILE HAS BEEN DELETED is eliminated too. Such a row cannot
-- come about in the normal flow (deleting a profile is refused if it has an
-- option bound to it, and the profile row is locked at that moment), but a
-- maintenance script that runs SQL directly, or a partial restore, can produce
-- one. The eligibility query has to be resilient to every row it reads: an
-- option of a profile whose shipping rule has vanished must not stand in the
-- storefront.
-- name: ListEligibleShippingOptions :many
SELECT shipping_options.* FROM shipping_options
JOIN shipping_profiles
  ON shipping_profiles.id = shipping_options.shipping_profile_id
 AND shipping_profiles.deleted_at IS NULL
WHERE shipping_options.deleted_at IS NULL
  AND (region_id = sqlc.arg('region_id')::text OR region_id = '')
  AND currency_code = sqlc.arg('currency_code')::text
  AND is_return = sqlc.arg('is_return')::boolean
  AND (sqlc.arg('include_admin_only')::boolean OR admin_only = FALSE)
  AND (cardinality(sqlc.arg('profile_ids')::text[]) = 0
       OR shipping_profile_id = ANY (sqlc.arg('profile_ids')::text[]))
ORDER BY shipping_options.id;

-- name: UpdateShippingOption :one
UPDATE shipping_options
SET name       = $2,
    price_type = $3,
    amount     = $4,
    region_id  = $5,
    is_return  = $6,
    admin_only = $7,
    data       = $8,
    metadata   = $9,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- SoftDeleteShippingOption SOFT deletes the option (plan Section 8).
--
-- A physical delete would hit the ON DELETE RESTRICT constraint of the
-- fulfillments bound to the option (fulfillments.shipping_option_id); a soft
-- delete takes the option out of the catalog without breaking history.
-- name: SoftDeleteShippingOption :one
UPDATE shipping_options
SET deleted_at = now(),
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;
