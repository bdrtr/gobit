-- customer_group and membership queries.

-- name: InsertCustomerGroup :one
INSERT INTO customer_group (id, name, rank, metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $5)
RETURNING *;

-- name: GetCustomerGroup :one
SELECT * FROM customer_group
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListCustomerGroups :many
SELECT * FROM customer_group
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountCustomerGroups :one
SELECT count(*) FROM customer_group
WHERE deleted_at IS NULL;

-- UpdateCustomerGroup leaves the fields that are not given AS THEY ARE.
--
-- The name has to be correctable: a name is unique among live groups, and a
-- mistyped name with no way to correct it would occupy that name forever.
-- name: UpdateCustomerGroup :one
UPDATE customer_group SET
    name       = COALESCE(sqlc.narg('name')::text, name),
    -- rank is nullable in the ARGUMENT and not in the column: a nil means "do
    -- not touch", which is what lets a merchant rename a group without silently
    -- resetting the order they set (ADR 0049).
    rank       = COALESCE(sqlc.narg('rank')::int, rank),
    metadata   = COALESCE(sqlc.narg('metadata')::jsonb, metadata),
    updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- SoftDeleteCustomerGroup soft-deletes the group.
--
-- The membership rows are LEFT in place: a deleted group shows up in no read
-- anyway (every query that reads a group filters on deleted_at IS NULL), and
-- the rows go by cascade the day the record is really deleted. The name becomes
-- reusable because it leaves the partial unique index's scope.
-- name: SoftDeleteCustomerGroup :one
UPDATE customer_group
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- AddCustomerToGroup writes the membership; if it already exists it does
-- nothing.
--
-- A membership is a SET: the same call arriving twice (a retry, a double
-- click) is not an error but the same outcome. ON CONFLICT DO NOTHING
-- expresses this idempotency in one line.
-- name: AddCustomerToGroup :exec
INSERT INTO customer_group_customer (customer_id, customer_group_id, created_at)
VALUES ($1, $2, $3)
ON CONFLICT (customer_id, customer_group_id) DO NOTHING;

-- RemoveCustomerFromGroup deletes the membership and returns the NUMBER OF
-- DELETED ROWS.
--
-- The number is the one piece of information that tells "there was no
-- membership" from "the membership was removed"; for a request to remove a
-- membership that does not exist, the service returns errors.NotFound.
-- name: RemoveCustomerFromGroup :execrows
DELETE FROM customer_group_customer
WHERE customer_id = $1 AND customer_group_id = $2;

-- ListGroupsOfCustomer returns a customer's groups IN ORDER.
--
-- The order is RANK, then id: the first group is the winner the merchant
-- chose. This order became a CONTRACT with ADR 0049 — the head of
-- Service.CustomerGroupIDs is the one group the cart writes into its rule
-- context. The previous order (created_at DESC, id DESC) was not arbitrary,
-- but it was not a PROMISE either; now it is a promise.
--
-- name: ListGroupsOfCustomer :many
SELECT g.* FROM customer_group g
JOIN customer_group_customer m ON m.customer_group_id = g.id
WHERE m.customer_id = $1 AND g.deleted_at IS NULL
ORDER BY g.rank, g.id;

-- ListGroupIDsOfCustomers returns the group ids of several customers in ONE
-- query.
--
-- The Query provider serves customers together with their group ids; a
-- separate query per customer would be the N+1 that ADR 0004 structurally
-- forbids.
-- name: ListGroupIDsOfCustomers :many
SELECT m.customer_id, m.customer_group_id
FROM customer_group_customer m
JOIN customer_group g ON g.id = m.customer_group_id
WHERE m.customer_id = ANY(@customer_ids::text[]) AND g.deleted_at IS NULL
ORDER BY m.customer_id, m.customer_group_id;

-- SetGroupSegment makes a group a segment with the given rule, or gives it a
-- new rule (ADR 0217). The rule's moment is new, so a pass that read the old
-- rule writes nothing more, and the group waits for its next evaluation.
-- name: SetGroupSegment :one
UPDATE customer_group
SET segment = sqlc.arg('segment'), segment_set_at = sqlc.arg('set_at'),
    segment_evaluated_at = NULL, updated_at = sqlc.arg('set_at')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- ClearGroupSegment hands a segment back to the operator; its members stay.
-- name: ClearGroupSegment :one
UPDATE customer_group
SET segment = NULL, segment_set_at = NULL, segment_evaluated_at = NULL, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- CountSegments counts the live segments other than the given group.
-- name: CountSegments :one
SELECT count(*) FROM customer_group
WHERE segment IS NOT NULL AND deleted_at IS NULL AND id <> $1;

-- ListSegments reads the live segments in id order.
-- name: ListSegments :many
SELECT * FROM customer_group
WHERE segment IS NOT NULL AND deleted_at IS NULL
ORDER BY id
LIMIT $1;

-- LockSegment locks a live segment's row and returns the moment of its rule;
-- no row is a group that is gone or is no segment any more.
-- name: LockSegment :one
SELECT segment_set_at FROM customer_group
WHERE id = $1 AND deleted_at IS NULL AND segment IS NOT NULL
FOR UPDATE;

-- RemoveSegmentStrays takes out of a segment the members whose id falls in
-- (after_id, last_id] and who are not among the given ones; an empty last_id
-- reaches the end of the ids.
-- name: RemoveSegmentStrays :execrows
DELETE FROM customer_group_customer
WHERE customer_group_id = sqlc.arg('group_id')
  AND customer_id > sqlc.arg('after_id')::text
  AND (sqlc.arg('last_id')::text = '' OR customer_id <= sqlc.arg('last_id')::text)
  AND NOT (customer_id = ANY(sqlc.arg('members')::text[]));

-- AddSegmentMembers puts the given live customers in a segment.
-- name: AddSegmentMembers :execrows
INSERT INTO customer_group_customer (customer_id, customer_group_id, created_at)
SELECT c.id, sqlc.arg('group_id'), sqlc.arg('created_at')
FROM customer c
WHERE c.id = ANY(sqlc.arg('members')::text[]) AND c.deleted_at IS NULL
ON CONFLICT (customer_id, customer_group_id) DO NOTHING;

-- FinishSegment records that a pass wrote a segment's members, only while the
-- rule it evaluated is still the segment's.
-- name: FinishSegment :execrows
UPDATE customer_group
SET segment_evaluated_at = sqlc.arg('evaluated_at')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL AND segment_set_at = sqlc.arg('set_at');

-- LockSegmentCount serializes the writers that turn a group into a segment, so
-- the count under MaxSegments is the count they all see. The key is the
-- repository's SegmentCountLockKey.
-- name: LockSegmentCount :exec
SELECT pg_advisory_xact_lock(sqlc.arg('lock_key')::bigint);
