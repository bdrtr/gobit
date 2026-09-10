-- store_profile queries: who the shop is.

-- GetStoreProfile reads the single row.
--
-- The identifier is passed rather than hardcoded here so the query stays a
-- statement about a table; the ONE row is the schema's rule (store_profile_singleton)
-- and the service holds the name.
-- name: GetStoreProfile :one
SELECT * FROM store_profile
WHERE id = $1;

-- UpsertStoreProfile writes the profile, replacing what was there.
--
-- One statement, not "read then insert or update": the two would race, and the
-- row this table holds is the one every document is printed against. A caller
-- sends the whole record because that is what an identity is -- a half-written
-- one is a document that names a shop that does not exist.
-- name: UpsertStoreProfile :one
INSERT INTO store_profile (
    id, legal_name, tax_number, tax_office, email, address, country_code
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id)
DO UPDATE SET
    legal_name   = EXCLUDED.legal_name,
    tax_number   = EXCLUDED.tax_number,
    tax_office   = EXCLUDED.tax_office,
    email        = EXCLUDED.email,
    address      = EXCLUDED.address,
    country_code = EXCLUDED.country_code,
    updated_at   = now()
RETURNING *;
