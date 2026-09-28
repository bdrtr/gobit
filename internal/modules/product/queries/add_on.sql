-- name: ListProductAddOns :many
-- A product's add-on variants, in the operator's order (ADR 0228).
SELECT variant_id FROM product_add_on
WHERE product_id = $1
ORDER BY rank;

-- name: DeleteProductAddOns :exec
DELETE FROM product_add_on WHERE product_id = $1;

-- name: InsertProductAddOns :exec
-- Writes a product's list; the position in the array is the rank.
INSERT INTO product_add_on (product_id, variant_id, rank)
SELECT sqlc.arg('product_id')::text, added.id, (added.ordinality - 1)::integer
FROM unnest(sqlc.arg('variant_ids')::text[]) WITH ORDINALITY AS added(id, ordinality);

-- name: DeleteProductAddOnsTouching :exec
-- Removes a product's list and every entry naming one of its variants; its
-- deletion calls this.
DELETE FROM product_add_on AS a
WHERE a.product_id = sqlc.arg('product_id')::text
   OR a.variant_id IN (SELECT v.id FROM product_variant AS v WHERE v.product_id = sqlc.arg('product_id')::text);

-- name: DeleteAddOnsOfVariant :exec
-- Removes every entry naming a variant; its deletion calls this.
DELETE FROM product_add_on WHERE variant_id = $1;

-- name: ListProductAddOnsOfProducts :many
-- Every add-on of the given products in one statement, for the read layer's
-- batch (ADR 0229).
SELECT product_id, variant_id FROM product_add_on
WHERE product_id = ANY(sqlc.arg('product_ids')::text[])
ORDER BY product_id, rank;
