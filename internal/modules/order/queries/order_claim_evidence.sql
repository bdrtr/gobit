-- order_claim_evidence queries.
--
-- Evidence is attached and detached, never edited: a caption corrected in place
-- would rewrite what an operator said at the time, and what a claim record is
-- for is what was said at the time.

-- name: CreateOrderClaimEvidence :one
INSERT INTO order_claim_evidence (id, order_claim_id, upload_id, caption)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListOrderClaimEvidence :many
SELECT * FROM order_claim_evidence
WHERE order_claim_id = $1
ORDER BY created_at, id;

-- name: DeleteOrderClaimEvidence :execrows
DELETE FROM order_claim_evidence
WHERE id = $1;
