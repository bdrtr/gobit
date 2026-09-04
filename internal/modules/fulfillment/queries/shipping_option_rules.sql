-- shipping_option_rules queries.
--
-- A rule determines UNDER WHICH CONDITION the option is offered. The condition
-- itself is not evaluated here; SQL only carries the rows, the matching is done
-- in the pure function in the service (see the same split with matchRule in the
-- pricing module).

-- name: CreateShippingOptionRule :one
INSERT INTO shipping_option_rules (id, shipping_option_id, attribute, operator, rule_values)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetShippingOptionRule :one
SELECT * FROM shipping_option_rules
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListShippingOptionRules :many
SELECT * FROM shipping_option_rules
WHERE shipping_option_id = $1 AND deleted_at IS NULL
ORDER BY id;

-- ListShippingOptionRulesByOptions returns the rules for MULTIPLE options in a
-- single round trip.
--
-- Being batched is what keeps the eligibility listing from doing N+1: issuing
-- as many queries as there are candidate options would be a price paid every
-- time the cart is updated.
-- name: ListShippingOptionRulesByOptions :many
SELECT * FROM shipping_option_rules
WHERE shipping_option_id = ANY (sqlc.arg('option_ids')::text[]) AND deleted_at IS NULL
ORDER BY shipping_option_id, id;

-- SoftDeleteShippingOptionRule SOFT deletes the rule (plan Section 8).
-- name: SoftDeleteShippingOptionRule :one
UPDATE shipping_option_rules
SET deleted_at = now(),
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;
