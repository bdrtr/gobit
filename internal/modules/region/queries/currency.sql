-- currency sorguları. Tüm okumalar deleted_at IS NULL filtresi uygular.

-- name: GetCurrency :one
SELECT * FROM currency
WHERE code = $1 AND deleted_at IS NULL;

-- name: ListCurrencies :many
SELECT * FROM currency
WHERE deleted_at IS NULL
ORDER BY code
LIMIT $1 OFFSET $2;

-- name: CountCurrencies :one
SELECT count(*) FROM currency
WHERE deleted_at IS NULL;

-- GetCurrenciesByCodes birden çok para birimini TEK turda okur.
--
-- Query sağlayıcısı bölgeleri para birimleriyle birlikte döndürür; kod başına
-- ayrı sorgu N+1 demek olurdu (ADR 0004'ün toplu okuma şartı).
-- name: GetCurrenciesByCodes :many
SELECT * FROM currency
WHERE code = ANY(@codes::text[]) AND deleted_at IS NULL
ORDER BY code;
