-- tax_class and tax_class_member queries. Every read filters on
-- deleted_at IS NULL.

-- name: InsertTaxClass :one
INSERT INTO tax_class (id, name, metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, $4)
RETURNING *;

-- name: GetTaxClass :one
SELECT * FROM tax_class
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListTaxClasses :many
SELECT * FROM tax_class
WHERE deleted_at IS NULL
ORDER BY name, id;

-- name: SoftDeleteTaxClass :one
UPDATE tax_class
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- InsertTaxClassMember binds a product to a class.
--
-- The product is the conflict target rather than the pair: a product belongs to
-- AT MOST ONE class, so binding it again MOVES it instead of failing. An
-- operator who reclassifies a product is doing the ordinary thing, and refusing
-- it would make them delete the old membership first for no reason.
-- name: InsertTaxClassMember :one
INSERT INTO tax_class_member (id, tax_class_id, product_id, created_at, updated_at)
VALUES ($1, $2, $3, $4, $4)
ON CONFLICT (product_id) WHERE deleted_at IS NULL
DO UPDATE SET tax_class_id = EXCLUDED.tax_class_id, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: ListTaxClassMembers :many
SELECT * FROM tax_class_member
WHERE tax_class_id = $1 AND deleted_at IS NULL
ORDER BY product_id;

-- ListTaxClassMembersByProducts resolves the class of EVERY product entering a
-- calculation in one query; however many lines a cart has, the number of round
-- trips stays constant (no N+1).
-- name: ListTaxClassMembersByProducts :many
SELECT * FROM tax_class_member
WHERE product_id = ANY(@product_ids::text[]) AND deleted_at IS NULL
ORDER BY product_id;

-- name: SoftDeleteTaxClassMember :one
UPDATE tax_class_member
SET deleted_at = $2, updated_at = $2
WHERE product_id = $1 AND deleted_at IS NULL
RETURNING *;

-- CountTaxClassMembers reports how many products a class still holds; the
-- delete reads it to refuse emptying a class out from under live rules.
-- name: CountTaxClassMembers :one
SELECT count(*) FROM tax_class_member
WHERE tax_class_id = $1 AND deleted_at IS NULL;
