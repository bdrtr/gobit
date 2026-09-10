-- promotion queries.

-- name: InsertPromotion :one
INSERT INTO promotion (
    id, code, is_automatic, type, campaign_id, status,
    usage_limit, usage_count, metadata, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, 0, $8, $9, $9)
RETURNING *;

-- name: GetPromotion :one
SELECT * FROM promotion
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetPromotionByCode :one
SELECT * FROM promotion
WHERE code = $1 AND deleted_at IS NULL;

-- name: ListPromotions :many
SELECT * FROM promotion
WHERE deleted_at IS NULL
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (sqlc.narg('campaign_id')::text IS NULL OR campaign_id = sqlc.narg('campaign_id')::text)
ORDER BY id
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountPromotions gives the total for the pagination envelope and applies the
-- SAME filters as ListPromotions; the two have to be changed together.
--
-- The total cannot be read from a window function returned alongside the rows:
-- an out-of-range page returns no rows at all, so the window is never evaluated
-- and the total would appear as 0.
-- name: CountPromotions :one
SELECT count(*) FROM promotion
WHERE deleted_at IS NULL
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (sqlc.narg('campaign_id')::text IS NULL OR campaign_id = sqlc.narg('campaign_id')::text);

-- GetPromotionsByIDs serves the Query layer's FetchByIDs call in ONE round trip.
-- name: GetPromotionsByIDs :many
SELECT * FROM promotion
WHERE id = ANY (@ids::text[]) AND deleted_at IS NULL
ORDER BY id;

-- ListApplicablePromotions returns, in ONE round trip, the promotions that may
-- enter the computation: among the active ones, those that are AUTOMATIC and
-- those carrying one of the given CODES.
--
-- The code set may be empty (automatics only); in PostgreSQL an ANY comparison
-- against an empty array selects no row, so no separate branch is needed.
--
-- Keeping the filter in SQL is deliberate: pulling every promotion and sieving
-- them in the application would mean reading the whole table on every cart
-- computation, and the cost would grow with the number of promotions.
-- name: ListApplicablePromotions :many
SELECT * FROM promotion
WHERE deleted_at IS NULL
  AND status = 'active'
  AND (is_automatic OR code = ANY (@codes::text[]))
ORDER BY id;

-- ListCandidatesForDiagnosis returns the same set WITHOUT the status filter.
--
-- It exists so that the admin computation can say WHY a promotion did not apply,
-- and the status is the answer a merchant needs most often: a coupon that was
-- published but never activated is the commonest reason a code does nothing. The
-- filtered query above cannot report it — a draft promotion is not a candidate at
-- all, so it comes back neither applied nor skipped, and the endpoint would
-- answer every question except the likely one.
--
-- What it does NOT drop is the rest of the population. `deleted_at IS NULL`
-- stays because a deleted promotion is GONE rather than skipped, and the
-- automatic-or-code filter stays because the alternative is every promotion in
-- the shop — an answer whose size grows with the catalogue and which says
-- nothing about the cart that was asked about.
--
-- The hot path does NOT use this. A cart's totals are recomputed on every change
-- and the status filter is what keeps that read proportional to the promotions
-- that could apply; this one is asked once, by an operator.
-- name: ListCandidatesForDiagnosis :many
SELECT * FROM promotion
WHERE deleted_at IS NULL
  AND (is_automatic OR code = ANY (@codes::text[]))
ORDER BY id;

-- UpdatePromotion updates the promotion's DEFINITION.
--
-- usage_count is DELIBERATELY left out: only the redemption flow moves that
-- counter (see IncrementPromotionUsage / DecrementPromotionUsage).
-- name: UpdatePromotion :one
UPDATE promotion
SET code         = $2,
    is_automatic = $3,
    type         = $4,
    campaign_id  = $5,
    status       = $6,
    usage_limit  = $7,
    metadata     = $8,
    updated_at   = $9
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeletePromotion :one
UPDATE promotion
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- LockPromotion locks the promotion for the duration of the transaction; it is
-- the FIRST step of the redemption flow.
--
-- There is a single lock order and every flow uses the same one: promotion
-- FIRST, campaign SECOND. Reversing that order means a deadlock; when two
-- promotions attached to the same campaign are redeemed concurrently both ask
-- for that same campaign row, and the order can only be guaranteed here.
-- name: LockPromotion :one
SELECT * FROM promotion
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- LockPromotionShared reads the promotion under a SHARED lock; it is the first
-- step of the paths that write rows UNDERNEATH it (adding a rule, writing an
-- application method).
--
-- The lock is MANDATORY and a foreign key does NOT take its place:
-- promotion_rule and promotion_application_method do reference promotion(id),
-- but deletion is SOFT and leaves the row in place. The FK check looks at the
-- EXISTENCE of the row, not at its deleted_at; that is why no constraint can
-- stop a row being written underneath a deleted promotion. Measured
-- (2026-09-06): a soft delete slipping in between the existence check and the
-- write let the write through without making it wait.
--
-- FOR SHARE is taken and not FOR UPDATE: two administrators must be able to add
-- a rule to the same promotion at the same time, and two FOR SHAREs do not
-- conflict. The deletion, on the other hand, is a plain UPDATE and puts a FOR
-- NO KEY UPDATE lock on the row — FOR SHARE DOES conflict with that, so the
-- write waits for the deletion and, once it has the lock, RE-EVALUATES the
-- WHERE condition and sees "no such record".
--
-- name: LockPromotionShared :one
SELECT * FROM promotion
WHERE id = $1 AND deleted_at IS NULL
FOR SHARE;

-- IncrementPromotionUsage raises the usage counter CONDITIONALLY.
--
-- If the limit would be exceeded the row is NOT UPDATED and the query returns
-- no row at all; the caller reads that as "the redemptions are used up" (see
-- the same argument at IncrementCampaignBudget).
-- name: IncrementPromotionUsage :one
UPDATE promotion
SET usage_count = usage_count + 1,
    updated_at  = @now::timestamptz
WHERE id = @id::text
  AND deleted_at IS NULL
  AND (usage_limit IS NULL OR usage_count + 1 <= usage_limit)
RETURNING *;

-- DecrementPromotionUsage lowers the usage counter and NEVER GOES BELOW ZERO.
-- The argument is the same as the one at DecrementCampaignBudget.
-- name: DecrementPromotionUsage :one
UPDATE promotion
SET usage_count = greatest(usage_count - 1, 0),
    updated_at  = @now::timestamptz
WHERE id = @id::text AND deleted_at IS NULL
RETURNING *;
