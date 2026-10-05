-- name: ListVariantCostsOf :many
-- The unit costs of every given variant, each variant's in currency order
-- (ADR 0401).
SELECT variant_id, currency_code, amount FROM product_variant_cost
WHERE variant_id = ANY(sqlc.arg('variant_ids')::text[])
ORDER BY variant_id, currency_code;

-- name: DeleteVariantCosts :exec
DELETE FROM product_variant_cost WHERE variant_id = $1;

-- name: InsertVariantCosts :exec
-- Writes a variant's unit costs, one row per currency; the two arrays pair up
-- by position.
INSERT INTO product_variant_cost (variant_id, currency_code, amount)
SELECT sqlc.arg('variant_id')::text, c.code, (sqlc.arg('amounts')::bigint[])[c.ordinality]
FROM unnest(sqlc.arg('currency_codes')::text[]) WITH ORDINALITY AS c(code, ordinality);
