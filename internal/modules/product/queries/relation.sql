-- name: ListProductRelations :many
-- A product's relations, every kind, each kind in the operator's order.
SELECT type, related_product_id FROM product_relation
WHERE product_id = $1
ORDER BY type, rank;

-- name: ListProductRelationsOfType :many
-- One kind of a product's relations, in the operator's order.
SELECT related_product_id FROM product_relation
WHERE product_id = $1 AND type = $2
ORDER BY rank;

-- name: DeleteProductRelationsOfType :exec
DELETE FROM product_relation WHERE product_id = $1 AND type = $2;

-- name: InsertProductRelations :exec
-- Writes one kind's list; the position in the array is the rank.
INSERT INTO product_relation (product_id, type, related_product_id, rank)
SELECT sqlc.arg('product_id')::text, sqlc.arg('type')::text, related.id, (related.ordinality - 1)::integer
FROM unnest(sqlc.arg('related_ids')::text[]) WITH ORDINALITY AS related(id, ordinality);

-- name: DeleteProductRelationsTouching :exec
-- Removes every relation from or to a product; its deletion calls this.
DELETE FROM product_relation WHERE product_id = $1 OR related_product_id = $1;
