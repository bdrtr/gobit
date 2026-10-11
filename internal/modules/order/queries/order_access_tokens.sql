-- order_access_tokens queries (ADR 0439).
--
-- The table holds digests only. Nothing here reads a token back, because no
-- plaintext is stored to read: a route that is handed one digests it and asks
-- which digests the order holds.

-- InsertOrderAccessToken records the digest of a token just issued.
-- name: InsertOrderAccessToken :exec
INSERT INTO order_access_tokens (order_id, token_hash)
VALUES (sqlc.arg('order_id'), sqlc.arg('token_hash'));

-- ListOrderAccessTokenHashes returns the digests of the tokens the order
-- issued, oldest first, so the comparison the caller makes walks every one.
-- name: ListOrderAccessTokenHashes :many
SELECT token_hash FROM order_access_tokens
WHERE order_id = $1
ORDER BY created_at, token_hash;

-- DeleteOrderAccessTokens withdraws every token of the orders an erasure
-- forgets, in the erasure's transaction.
-- name: DeleteOrderAccessTokens :execrows
DELETE FROM order_access_tokens
WHERE order_id = ANY (sqlc.arg('order_ids')::text[]);
