-- shipping_profiles queries.
--
-- A profile is the container of the shipping options and does NOT KNOW which
-- products it is bound to: the product-profile link is established over Module
-- Links (Principle 2.1/2.2).

-- name: CreateShippingProfile :one
INSERT INTO shipping_profiles (id, name, type, metadata)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetShippingProfile :one
SELECT * FROM shipping_profiles
WHERE id = $1 AND deleted_at IS NULL;

-- LockShippingProfile reads the profile with a write lock held UNTIL THE END OF
-- THE TRANSACTION.
--
-- Because a soft delete updates a NON-key column, on its own it takes only
-- FOR NO KEY UPDATE; that lock does NOT CONFLICT with the FOR KEY SHARE an
-- option INSERT takes for the foreign key. That is, a CreateShippingOption
-- landing between the "does it have an option" check and the delete completes
-- without waiting, and a LIVE option bound to a deleted profile would be left
-- behind (under READ COMMITTED a single transaction does not prevent this on
-- its own). FOR UPDATE is the lock that establishes that conflict.
-- name: LockShippingProfile :one
SELECT * FROM shipping_profiles
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- LockShippingProfileShared reads the profile with a shared lock held UNTIL THE
-- END OF THE TRANSACTION.
--
-- The option creation path uses this: option insertions running in parallel
-- with one another do NOT WAIT (FOR SHARE does not conflict with itself), but
-- it does conflict with LockShippingProfile, which is trying to delete the
-- profile. The two paths are serialized this way.
-- name: LockShippingProfileShared :one
SELECT * FROM shipping_profiles
WHERE id = $1 AND deleted_at IS NULL
FOR SHARE;

-- name: ListShippingProfiles :many
SELECT * FROM shipping_profiles
WHERE deleted_at IS NULL
  AND (sqlc.narg('type')::text IS NULL OR type = sqlc.narg('type')::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountShippingProfiles gives the total count of the pagination envelope and
-- applies the SAME filters as ListShippingProfiles; the two have to be changed
-- together.
--
-- The total cannot be read from a window function returned along with the rows:
-- on an out-of-range page no rows are returned at all, the window is not
-- evaluated and the total would appear as 0. The total is the count of the
-- FILTER, not of the page.
-- name: CountShippingProfiles :one
SELECT COUNT(*) FROM shipping_profiles
WHERE deleted_at IS NULL
  AND (sqlc.narg('type')::text IS NULL OR type = sqlc.narg('type')::text);

-- name: UpdateShippingProfile :one
UPDATE shipping_profiles
SET name       = $2,
    type       = $3,
    metadata   = $4,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- SoftDeleteShippingProfile SOFT deletes the profile (plan Section 8).
--
-- The row physically remains: when the profile of an option that has
-- fulfillments is deleted, it must stay possible to read whom the historical
-- records belong to.
-- name: SoftDeleteShippingProfile :one
UPDATE shipping_profiles
SET deleted_at = now(),
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- CountAliveOptionsByProfile counts the LIVING options bound to the profile.
--
-- It is for the check before a delete: deleting a profile that still has an
-- option standing would silently do away with the shipping rule of the
-- products.
-- name: CountAliveOptionsByProfile :one
SELECT COUNT(*) FROM shipping_options
WHERE shipping_profile_id = $1 AND deleted_at IS NULL;
