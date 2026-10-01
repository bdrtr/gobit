-- SwitchPromotionStatus moves the promotion from the status the caller read to
-- another, and only if it is still in the first (ADR 0312): an operator who
-- paused a coupon another operator had already paused, or published a draft
-- somebody had already withdrawn, is told so rather than undoing the other.
-- name: SwitchPromotionStatus :one
UPDATE promotion
SET status     = sqlc.arg('to_status')::text,
    updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id')
  AND status = sqlc.arg('from_status')::text
  AND deleted_at IS NULL
RETURNING *;
