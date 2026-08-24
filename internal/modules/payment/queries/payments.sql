-- payments sorguları.
--
-- Bir oturumdan EN FAZLA BİR tahsilat çıkar (payments_session_uniq). Capture'ı
-- idempotent yapan şey budur: ikinci çağrı yeni satır yazmaz,
-- GetPaymentBySession ile var olanı bulur ve onu döner.

-- name: CreatePayment :one
INSERT INTO payments (
    id, payment_session_id, payment_collection_id, amount, currency_code, captured_at
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetPayment :one
SELECT * FROM payments
WHERE id = $1 AND deleted_at IS NULL;

-- LockPayment tahsilatı işlem boyunca kilitler; iade edilen tutar yalnızca bu
-- kilit altında güncellenir. Kilit sırasında koleksiyon ve oturumdan SONRA
-- gelir (bkz. service.Store "Kilit sırası").
-- name: LockPayment :one
SELECT * FROM payments
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: GetPaymentBySession :one
SELECT * FROM payments
WHERE payment_session_id = $1 AND deleted_at IS NULL;

-- name: ListPaymentsByCollection :many
SELECT * FROM payments
WHERE payment_collection_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC;

-- name: UpdatePaymentRefundedAmount :one
UPDATE payments
SET refunded_amount = $2,
    updated_at      = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;
