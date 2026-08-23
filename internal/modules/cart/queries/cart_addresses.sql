-- cart_addresses sorguları.
--
-- Sepet başına her türden (shipping/billing) en fazla bir YAŞAYAN adres olur;
-- kısıt cart_addresses_cart_type_uniq indeksindedir.

-- UpsertCartAddress adresi yazar; aynı türden bir adres varsa ÜZERİNE yazar.
--
-- Çakışma hedefi kısmi benzersiz indekstir, bu yüzden WHERE yan tümcesi
-- indeksinkiyle BİREBİR aynı olmalıdır; aksi hâlde PostgreSQL indeksi
-- çıkarsayamaz ve sorgu hata verir.
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
