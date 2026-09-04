-- cart_addresses queries.
--
-- There is at most one LIVING address of each kind (shipping/billing) per
-- cart; the constraint lives in the cart_addresses_cart_type_uniq index.

-- UpsertCartAddress writes the address; if one of the same kind exists it is
-- OVERWRITTEN.
--
-- The conflict target is a partial unique index, so the WHERE clause must be
-- EXACTLY the same as the index's; otherwise PostgreSQL cannot infer the index
-- and the query errors out.
-- name: UpsertCartAddress :one
INSERT INTO cart_addresses (
    id, cart_id, address_type, source_address_id,
    first_name, last_name, company,
    address_1, address_2, city, province, postal_code, country_code, phone,
    metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (cart_id, address_type) WHERE deleted_at IS NULL
DO UPDATE SET
    source_address_id = EXCLUDED.source_address_id,
    first_name        = EXCLUDED.first_name,
    last_name         = EXCLUDED.last_name,
    company           = EXCLUDED.company,
    address_1         = EXCLUDED.address_1,
    address_2         = EXCLUDED.address_2,
    city              = EXCLUDED.city,
    province          = EXCLUDED.province,
    postal_code       = EXCLUDED.postal_code,
    country_code      = EXCLUDED.country_code,
    phone             = EXCLUDED.phone,
    metadata          = EXCLUDED.metadata,
    updated_at        = now()
RETURNING *;

-- name: ListCartAddresses :many
SELECT * FROM cart_addresses
WHERE cart_id = $1 AND deleted_at IS NULL
ORDER BY address_type;

-- name: SoftDeleteCartAddressesByCart :exec
UPDATE cart_addresses
SET deleted_at = now(), updated_at = now()
WHERE cart_id = $1 AND deleted_at IS NULL;
