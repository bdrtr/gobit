-- ReviseCampaign writes the campaign's name, description, window and budget
-- limit only while they are still the ones the caller read (ADR 0331): one
-- conditional UPDATE, so a revision made meanwhile is never written over.
--
-- The business identifier and the budget's unit are not among them, and
-- budget_used stays the redemption flow's. A limit is written only beside a
-- budget type and an open one only without, as campaign_budget_none_check
-- and the service's budget rule say; a row the statement leaves alone is
-- explained by the service from the campaign as it is.
-- name: ReviseCampaign :one
UPDATE campaign
SET name         = sqlc.arg('name')::text,
    description  = sqlc.arg('description')::text,
    starts_at    = sqlc.narg('starts_at')::timestamptz,
    ends_at      = sqlc.narg('ends_at')::timestamptz,
    budget_limit = sqlc.narg('budget_limit')::bigint,
    updated_at   = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
  AND name = sqlc.arg('read_name')::text
  AND description = sqlc.arg('read_description')::text
  AND starts_at IS NOT DISTINCT FROM sqlc.narg('read_starts_at')::timestamptz
  AND ends_at IS NOT DISTINCT FROM sqlc.narg('read_ends_at')::timestamptz
  AND budget_limit IS NOT DISTINCT FROM sqlc.narg('read_budget_limit')::bigint
  AND (budget_type = 'none') = (sqlc.narg('budget_limit')::bigint IS NULL)
RETURNING *;
