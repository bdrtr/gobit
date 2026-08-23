-- customer_address sorguları. Tüm okumalar deleted_at IS NULL filtresi uygular.

-- name: InsertCustomerAddress :one
INSERT INTO customer_address (
    id, customer_id, first_name, last_name, company,
    address_1, address_2, city, country_code, postal_code, phone,
    is_default_shipping, is_default_billing, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)
RETURNING *;

-- GetCustomerAddress adresi kimliğiyle ve SAHİBİYLE birlikte okur.
--
-- customer_id koşulu bilinçlidir: bir müşterinin adres kimliğini tahmin eden
-- istek, sahiplik denetimi sorgunun dışında bırakılsaydı başkasının adresini
-- okuyabilirdi. Denetim WHERE'de olduğu sürece atlanamaz.
-- name: GetCustomerAddress :one
SELECT * FROM customer_address
WHERE id = $1 AND customer_id = $2 AND deleted_at IS NULL;

-- name: ListCustomerAddresses :many
SELECT * FROM customer_address
WHERE customer_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC;

-- name: UpdateCustomerAddress :one
UPDATE customer_address SET
    first_name   = COALESCE(sqlc.narg('first_name')::text, first_name),
    last_name    = COALESCE(sqlc.narg('last_name')::text, last_name),
    company      = COALESCE(sqlc.narg('company')::text, company),
    address_1    = COALESCE(sqlc.narg('address_1')::text, address_1),
    address_2    = COALESCE(sqlc.narg('address_2')::text, address_2),
    city         = COALESCE(sqlc.narg('city')::text, city),
    country_code = COALESCE(sqlc.narg('country_code')::text, country_code),
    postal_code  = COALESCE(sqlc.narg('postal_code')::text, postal_code),
    phone        = COALESCE(sqlc.narg('phone')::text, phone),
    updated_at   = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND customer_id = sqlc.arg('customer_id') AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteCustomerAddress :one
UPDATE customer_address
SET deleted_at = $3, updated_at = $3
WHERE id = $1 AND customer_id = $2 AND deleted_at IS NULL
RETURNING id;

-- ClearDefaultShipping müşterinin varsayılan kargo işaretini kaldırır.
--
-- Yeni varsayılanı yazmadan ÖNCE çalışmalıdır: kısmi benzersiz indeks müşteri
-- başına tek işaretli satıra izin verir ve temizleme atlanırsa ikinci
-- işaretleme benzersizlik ihlaliyle döner.
-- name: ClearDefaultShipping :exec
UPDATE customer_address
SET is_default_shipping = FALSE, updated_at = $2
WHERE customer_id = $1 AND is_default_shipping AND deleted_at IS NULL;

-- name: MarkDefaultShipping :one
UPDATE customer_address
SET is_default_shipping = TRUE, updated_at = $3
WHERE id = $1 AND customer_id = $2 AND deleted_at IS NULL
RETURNING *;

-- name: ClearDefaultBilling :exec
UPDATE customer_address
SET is_default_billing = FALSE, updated_at = $2
WHERE customer_id = $1 AND is_default_billing AND deleted_at IS NULL;

-- name: MarkDefaultBilling :one
UPDATE customer_address
SET is_default_billing = TRUE, updated_at = $3
WHERE id = $1 AND customer_id = $2 AND deleted_at IS NULL
RETURNING *;
