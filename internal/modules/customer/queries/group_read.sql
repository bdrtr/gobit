-- GetCustomerGroupsByIDs reads the live groups of the ids in one round, for
-- the read layer's fetch by id (ADR 0321): a promotion rule names its groups
-- by id, and the panel names them back.
-- name: GetCustomerGroupsByIDs :many
SELECT * FROM customer_group
WHERE id = ANY(sqlc.arg('ids')::text[]) AND deleted_at IS NULL
ORDER BY id;
