-- b2b_company sorguları. Tüm okumalar deleted_at IS NULL filtresi uygular.

-- name: InsertCompany :one
INSERT INTO b2b_company (
    id, name, email, phone, address, city, postal_code, country_code,
    currency_code, spending_limit_reset_period, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
RETURNING *;

-- name: GetCompany :one
SELECT * FROM b2b_company
WHERE id = $1 AND deleted_at IS NULL;

-- ListCompanies süzgeçlenmiş ve sayfalanmış şirket listesini döner.
--
-- E-posta süzgeci BİRDEN ÇOK satır döndürebilir: şirket e-postası benzersiz
-- değildir (gerekçe migration'daki tablo belgesindedir).
-- name: ListCompanies :many
SELECT * FROM b2b_company
WHERE deleted_at IS NULL
  AND (sqlc.narg('email')::text IS NULL OR email = sqlc.narg('email')::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountCompanies :one
SELECT count(*) FROM b2b_company
WHERE deleted_at IS NULL
  AND (sqlc.narg('email')::text IS NULL OR email = sqlc.narg('email')::text);

-- UpdateCompany verilmeyen alanları OLDUĞU GİBİ bırakır.
--
-- COALESCE ile yazılan bu kısmi güncelleme "alan gönderilmedi" ile "alan boşa
-- çekildi" ayrımını korur: NULL parametre eski değeri saklar, boş dize gerçek
-- bir temizlemedir. Adres alanları için bu ayrım somuttur — taşınan bir
-- şirketin eski posta kodu silinebilmelidir.
-- name: UpdateCompany :one
UPDATE b2b_company SET
    name                        = COALESCE(sqlc.narg('name')::text, name),
    email                       = COALESCE(sqlc.narg('email')::text, email),
    phone                       = COALESCE(sqlc.narg('phone')::text, phone),
    address                     = COALESCE(sqlc.narg('address')::text, address),
    city                        = COALESCE(sqlc.narg('city')::text, city),
    postal_code                 = COALESCE(sqlc.narg('postal_code')::text, postal_code),
    country_code                = COALESCE(sqlc.narg('country_code')::text, country_code),
    currency_code               = COALESCE(sqlc.narg('currency_code')::text, currency_code),
    spending_limit_reset_period = COALESCE(sqlc.narg('reset_period')::text, spending_limit_reset_period),
    updated_at                  = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- SoftDeleteCompany şirketi yumuşak siler.
--
-- ÇALIŞANLARI da aynı işlemde silinir (bkz. SoftDeleteEmployeesOfCompany);
-- ikisinin ayrı çağrılarda yapılması, arada kalan bir hatada şirketsiz çalışan
-- kayıtları bırakırdı ve o kayıtlar vitrinde silinmiş bir şirketi gösterirdi.
-- name: SoftDeleteCompany :one
UPDATE b2b_company
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;
