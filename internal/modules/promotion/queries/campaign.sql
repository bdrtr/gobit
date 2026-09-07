-- campaign queries.

-- name: InsertCampaign :one
INSERT INTO campaign (
    id, name, campaign_identifier, description, starts_at, ends_at,
    budget_type, budget_limit, budget_used, budget_currency_code,
    created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, $9, $10, $10)
RETURNING *;

-- name: GetCampaign :one
SELECT * FROM campaign
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetCampaignByIdentifier :one
SELECT * FROM campaign
WHERE campaign_identifier = $1 AND deleted_at IS NULL;

-- name: ListCampaigns :many
SELECT * FROM campaign
WHERE deleted_at IS NULL
ORDER BY id
LIMIT $1 OFFSET $2;

-- name: CountCampaigns :one
SELECT count(*) FROM campaign
WHERE deleted_at IS NULL;

-- GetCampaignsByIDs fetches the campaign metadata of a promotion listing in ONE
-- round trip; no query is issued per campaign (N+1).
-- name: GetCampaignsByIDs :many
SELECT * FROM campaign
WHERE id = ANY (@ids::text[]) AND deleted_at IS NULL
ORDER BY id;

-- UpdateCampaign updates the campaign's DEFINITION.
--
-- budget_used is DELIBERATELY left out: only the redemption flow
-- (IncrementCampaignBudget / DecrementCampaignBudget) moves that counter. If it
-- could be written from the administration surface it would race a concurrent
-- redemption and undo the counter.
--
-- While the counter is NOT zero the budget's UNIT (its type and its currency)
-- is frozen; that is what the condition in the WHERE says. The counter is held
-- in one unit, and if that unit changed THE COUNTER ITSELF would stay in the
-- old one: turn a "usage" campaign (limit 100, 30 REDEMPTIONS spent) into
-- "spend" and the 30 in the counter now counts as 30 MINOR UNITS of money; and
-- when the currency changes, spending previously made in TRY is read as USD
-- while ongoing TRY redemptions start being refused with
-- campaign_budget_currency_mismatch. Both are a silent corruption of the
-- accounting.
--
-- Keeping the condition in the WHERE is deliberate: with a "read first, then
-- write" in the application, a redemption slipping in between the two
-- statements would take the counter off zero and the update would still go
-- through. An operator who wants to change the unit without the counter being
-- zero has to release the redemptions first.
-- name: UpdateCampaign :one
UPDATE campaign
SET name                 = $2,
    campaign_identifier  = $3,
    description          = $4,
    starts_at            = $5,
    ends_at              = $6,
    budget_type          = $7,
    budget_limit         = $8,
    budget_currency_code = $9,
    updated_at           = $10
WHERE id = $1
  AND deleted_at IS NULL
  AND (
      budget_used = 0
      OR (budget_type = $7 AND budget_currency_code IS NOT DISTINCT FROM $9)
  )
RETURNING *;

-- name: SoftDeleteCampaign :one
UPDATE campaign
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- LockCampaign locks the campaign for the duration of the transaction.
--
-- THIS IS THE FOUNDATION OF CONCURRENT REDEMPTION. Two concurrent Redeems have
-- to lock the same row; the second waits until the first one's transaction is
-- over and then, under READ COMMITTED, sees the row's CURRENT budget. That is
-- why a "read first, then write" race cannot form here: the read already
-- happens behind the lock.
--
-- There is a single lock order and every flow uses the same one: promotion
-- FIRST, campaign SECOND. Reversing that order means a deadlock.
-- name: LockCampaign :one
SELECT * FROM campaign
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- IncrementCampaignBudget raises the budget counter CONDITIONALLY.
--
-- If the limit would be exceeded the row is NOT UPDATED and the query returns
-- no row at all; the caller reads that as "the budget did not suffice". Keeping
-- the condition in the WHERE is deliberate: checking the limit in the
-- application and then issuing a separate UPDATE would, on a path that took no
-- lock, let another redemption slip in between the two statements.
-- name: IncrementCampaignBudget :one
UPDATE campaign
SET budget_used = budget_used + @delta::bigint,
    updated_at  = @now::timestamptz
WHERE id = @id::text
  AND deleted_at IS NULL
  AND (budget_limit IS NULL OR budget_used + @delta::bigint <= budget_limit)
RETURNING *;

-- DecrementCampaignBudget lowers the budget counter and NEVER GOES BELOW ZERO.
--
-- The greatest(...) is a defence: in a state where the ledger and the counter
-- have drifted apart (a hand-run SQL statement, a partial restore) the reversal
-- must not write a negative budget. A negative budget would hit the CHECK
-- constraint and bring down the release — that is, a SAGA COMPENSATION.
-- name: DecrementCampaignBudget :one
UPDATE campaign
SET budget_used = greatest(budget_used - @delta::bigint, 0),
    updated_at  = @now::timestamptz
WHERE id = @id::text AND deleted_at IS NULL
RETURNING *;
