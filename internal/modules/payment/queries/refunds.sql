-- refunds sorguları.
--
-- Kısmi iade birden çok satır üretir; toplamı payments.refunded_amount
-- sütununda tutulur ve iki değer aynı işlemde, tahsilatın kilidi altında
-- yazılır.

-- name: CreateRefund :one
INSERT INTO refunds (
    id, payment_id, amount, reason
) VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetRefund :one
SELECT * FROM refunds
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListRefundsByPayment :many
SELECT * FROM refunds
WHERE payment_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC;
