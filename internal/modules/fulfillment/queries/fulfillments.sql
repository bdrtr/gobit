-- fulfillments queries.
--
-- The fulfillment row is written BEFORE GOING to the provider: the Reference
-- field of the provider contract is "the id the caller gave to its own record",
-- and it is what matches the two systems up during reconciliation. Had the
-- provider been called first, a lost response would leave behind a shipping
-- label whose corresponding record could not be known.

-- InsertFulfillmentIfAbsent writes the fulfillment only if that idempotency key
-- has NOT BEEN USED YET.
--
-- On a conflict NO row is returned (pgx.ErrNoRows); the caller then reads the
-- fulfillment that exists under the key. A concurrent call slipping between the
-- two steps of "read first, write if absent" would hit the unique index and
-- WOULD ABORT THE TRANSACTION; ON CONFLICT DO NOTHING reduces that race to a
-- single statement and the losing side WAITS until the winner's transaction
-- finishes — so the row it reads is already completed with the provider's
-- response.
-- name: InsertFulfillmentIfAbsent :one
INSERT INTO fulfillments (
    id, reference, shipping_option_id, provider_id, status, idempotency_key,
    data, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (idempotency_key) WHERE deleted_at IS NULL DO NOTHING
RETURNING *;

-- name: GetFulfillment :one
SELECT * FROM fulfillments
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetFulfillmentByIdempotencyKey :one
SELECT * FROM fulfillments
WHERE idempotency_key = $1 AND deleted_at IS NULL;

-- LockFulfillment locks the fulfillment for the duration of the transaction and
-- returns its current state.
--
-- State transitions (cancel, ship, deliver) are made only under this lock: a
-- state read without the lock can be stale by the moment of the write, and two
-- calls canceling the same fulfillment at the same time would go to the
-- provider TWICE.
-- name: LockFulfillment :one
SELECT * FROM fulfillments
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: ListFulfillments :many
SELECT * FROM fulfillments
WHERE deleted_at IS NULL
  AND (sqlc.narg('reference')::text IS NULL OR reference = sqlc.narg('reference')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- GetFulfillmentsByIDs serves the Query layer's FetchByIDs call in a SINGLE
-- round trip; no query per id (N+1) is made.
-- name: GetFulfillmentsByIDs :many
SELECT * FROM fulfillments
WHERE id = ANY (sqlc.arg('ids')::text[]) AND deleted_at IS NULL
ORDER BY id;

-- CountFulfillments applies the SAME filters as ListFulfillments; for the
-- rationale see CountShippingProfiles.
-- name: CountFulfillments :one
SELECT COUNT(*) FROM fulfillments
WHERE deleted_at IS NULL
  AND (sqlc.narg('reference')::text IS NULL OR reference = sqlc.narg('reference')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text);

-- UpdateFulfillmentProviderResult writes the provider's response onto the row.
--
-- The provider id, the tracking information and the raw data are written as
-- ABSOLUTE values: an incremental update would pull the value the deciding code
-- saw apart from the value that gets written.
-- name: UpdateFulfillmentProviderResult :one
UPDATE fulfillments
SET external_id      = $2,
    status           = $3,
    tracking_number  = $4,
    tracking_url     = $5,
    data             = $6,
    shipped_at       = $7,
    delivered_at     = $8,
    canceled_at      = $9,
    updated_at       = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- UpdateFulfillmentStatus writes the status and the timestamp that accompanies
-- it.
--
-- The stamps are given as ABSOLUTE values; the constraints in the schema
-- (fulfillments_*_stamp) reject a write that leaves the status without its
-- stamp.
-- name: UpdateFulfillmentStatus :one
UPDATE fulfillments
SET status          = $2,
    tracking_number = $3,
    tracking_url    = $4,
    shipped_at      = $5,
    delivered_at    = $6,
    canceled_at     = $7,
    updated_at      = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;
