-- name: ListBundleComponentsOfVariants :many
-- The components of every given bundle variant, each bundle in the operator's
-- order (ADR 0234).
SELECT bundle_variant_id, component_variant_id, quantity FROM product_bundle_component
WHERE bundle_variant_id = ANY(sqlc.arg('bundle_ids')::text[])
ORDER BY bundle_variant_id, rank;

-- name: DeleteBundleComponents :exec
DELETE FROM product_bundle_component WHERE bundle_variant_id = $1;

-- name: InsertBundleComponents :exec
-- Writes a bundle's composition; the position in the arrays is the rank.
INSERT INTO product_bundle_component (bundle_variant_id, component_variant_id, quantity, rank)
SELECT sqlc.arg('bundle_id')::text, c.id, (sqlc.arg('quantities')::integer[])[c.ordinality],
       (c.ordinality - 1)::integer
FROM unnest(sqlc.arg('component_ids')::text[]) WITH ORDINALITY AS c(id, ordinality);

-- name: ListBundlesContaining :many
-- The bundle variants that hold any of the given variants as a component. A
-- bundle's rows go in its own deletion's transaction, so every row is a live
-- bundle's.
SELECT component_variant_id, bundle_variant_id FROM product_bundle_component
WHERE component_variant_id = ANY(sqlc.arg('component_ids')::text[])
ORDER BY component_variant_id, bundle_variant_id;

-- name: DeleteBundleComponentsOfProduct :exec
-- Removes the compositions of a product's bundle variants; its deletion calls
-- this.
DELETE FROM product_bundle_component AS c
WHERE c.bundle_variant_id IN (SELECT v.id FROM product_variant AS v WHERE v.product_id = sqlc.arg('product_id')::text);

-- name: LockLiveVariantsForBundle :many
-- The live variants among the given ones, with what a bundle write checks,
-- locked in id order: a composition written and a component deleted at the same
-- instant wait on the same row, and the one that waits re-reads it (ADR 0234).
SELECT v.id, v.product_id, v.manage_inventory, v.allow_backorder, p.is_giftcard
FROM product_variant AS v JOIN product AS p ON p.id = v.product_id
WHERE v.id = ANY(sqlc.arg('ids')::text[]) AND v.deleted_at IS NULL AND p.deleted_at IS NULL
ORDER BY v.id
FOR NO KEY UPDATE OF v;
