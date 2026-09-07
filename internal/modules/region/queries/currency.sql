-- currency queries.
--
-- The table has NO deleted_at and these reads carry NO such filter; the
-- argument is the same one at the top of country.sql and it is written out in
-- 000003.

-- name: GetCurrency :one
SELECT * FROM currency
WHERE code = $1;

-- name: ListCurrencies :many
SELECT * FROM currency
ORDER BY code
LIMIT $1 OFFSET $2;

-- name: CountCurrencies :one
SELECT count(*) FROM currency;

-- GetCurrenciesByCodes reads several currencies in ONE round trip.
--
-- The query provider returns regions together with their currencies; a separate
-- query per code would be an N+1 (ADR 0004's batch-read requirement).
-- name: GetCurrenciesByCodes :many
SELECT * FROM currency
WHERE code = ANY(@codes::text[])
ORDER BY code;
