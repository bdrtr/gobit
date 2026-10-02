-- ReviseStoreProfile writes the profile only while it is the one the caller
-- read, named by the moment it was last written (ADR 0336). The form writes
-- every field, so that moment is exactly what the caller read; a profile
-- written in between leaves the row alone.
-- name: ReviseStoreProfile :one
UPDATE store_profile
SET legal_name   = sqlc.arg('legal_name'),
    tax_number   = sqlc.arg('tax_number'),
    tax_office   = sqlc.arg('tax_office'),
    email        = sqlc.arg('email'),
    address      = sqlc.arg('address'),
    country_code = sqlc.arg('country_code'),
    updated_at   = now()
WHERE id = sqlc.arg('id')
  AND updated_at = sqlc.arg('read_updated_at')::timestamptz
RETURNING *;

-- CreateStoreProfile writes the first profile, and writes nothing when one
-- was written in between (ADR 0336).
-- name: CreateStoreProfile :one
INSERT INTO store_profile (
    id, legal_name, tax_number, tax_office, email, address, country_code
)
VALUES (
    sqlc.arg('id'), sqlc.arg('legal_name'), sqlc.arg('tax_number'), sqlc.arg('tax_office'),
    sqlc.arg('email'), sqlc.arg('address'), sqlc.arg('country_code')
)
ON CONFLICT (id) DO NOTHING
RETURNING *;
