-- notification_deliveries queries — THE DELIVERY LOG.
--
-- The log is written in two steps: the record is opened BEFORE the send
-- (Claim), its outcome is written AFTER the send (Finish). Writing it in a
-- single step — that is, sending first and recording afterwards — would have
-- opened the door to a duplicate notification: both concurrent handlers go to
-- the provider, and the uniqueness violation would only become visible after
-- TWO emails had gone out.

-- ClaimNotificationDelivery writes the record only if that (template,
-- reference) pair has NOT BEEN USED YET.
--
-- In case of a conflict NO ROW is returned (pgx.ErrNoRows) and this is not an
-- error: the caller then SKIPS the send. A concurrent call stepping between the
-- two steps of "read first, write if absent" would have hit the unique index;
-- ON CONFLICT DO NOTHING reduces the race to a single statement and lets the
-- database pick the winner.
-- name: ClaimNotificationDelivery :one
INSERT INTO notification_deliveries (
    id, template, channel, reference, provider_id, status
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (template, reference) DO NOTHING
RETURNING *;

-- FinishNotificationDelivery writes the outcome of the send attempt.
--
-- The status is written with an ABSOLUTE value, not with an incremental
-- transition: the writing code holds the outcome in hand and does not decide
-- according to the row it read.
-- name: FinishNotificationDelivery :one
UPDATE notification_deliveries
SET status     = $2,
    error      = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetNotificationDelivery :one
SELECT * FROM notification_deliveries
WHERE id = $1;

-- name: ListNotificationDeliveries :many
SELECT * FROM notification_deliveries
WHERE (sqlc.narg('reference')::text IS NULL OR reference = sqlc.narg('reference')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountNotificationDeliveries gives the total count of the pagination envelope
-- and applies THE SAME filters as ListNotificationDeliveries; the two have to
-- be changed together.
--
-- The total cannot be read from a window function returned together with the
-- rows: on an out-of-range page no row is returned, the window is not
-- evaluated and the total would appear as 0. The total is the count not of the
-- page but of the FILTER.
-- name: CountNotificationDeliveries :one
SELECT COUNT(*) FROM notification_deliveries
WHERE (sqlc.narg('reference')::text IS NULL OR reference = sqlc.narg('reference')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text);
