-- SetPromotionCampaign puts the promotion into a live campaign, or out of any
-- when the campaign is null, and only if it is still in the one the caller
-- read (ADR 0320): an operator moving a coupon another operator has just moved
-- is told so rather than undoing the other. A soft-deleted campaign is not
-- live, and putting a promotion into one would have the computation skip it
-- without anybody choosing that.
-- name: SetPromotionCampaign :one
UPDATE promotion
SET campaign_id = sqlc.narg('to_campaign_id')::text,
    updated_at  = sqlc.arg('updated_at')
WHERE promotion.id = sqlc.arg('id')
  AND promotion.campaign_id IS NOT DISTINCT FROM sqlc.narg('from_campaign_id')::text
  AND promotion.deleted_at IS NULL
  AND (sqlc.narg('to_campaign_id')::text IS NULL OR EXISTS (
      SELECT 1 FROM campaign
      WHERE campaign.id = sqlc.narg('to_campaign_id')::text
        AND campaign.deleted_at IS NULL))
RETURNING *;
