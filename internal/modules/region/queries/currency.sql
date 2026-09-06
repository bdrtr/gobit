-- currency sorguları.
--
-- Tabloda deleted_at YOKTUR ve okumalar öyle bir süzgeç TAŞIMAZ; gerekçe
-- country.sql'in başındaki ile aynıdır ve 000003'te yazılıdır.

-- name: GetCurrency :one
SELECT * FROM currency
WHERE code = $1;

-- name: ListCurrencies :many
SELECT * FROM currency
ORDER BY code
LIMIT $1 OFFSET $2;

-- name: CountCurrencies :one
SELECT count(*) FROM currency;

-- GetCurrenciesByCodes birden çok para birimini TEK turda okur.
--
-- Query sağlayıcısı bölgeleri para birimleriyle birlikte döndürür; kod başına
-- ayrı sorgu N+1 demek olurdu (ADR 0004'ün toplu okuma şartı).
-- name: GetCurrenciesByCodes :many
SELECT * FROM currency
WHERE code = ANY(@codes::text[])
ORDER BY code;
