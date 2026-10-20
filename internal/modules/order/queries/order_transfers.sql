-- order_transfers queries (ADR 0450).
--
-- A transfer row is written once and closed once. Which order it moves and to
-- whom never change; the status leaves 'requested' exactly once, under the
-- order's lock, and the WHERE below is the second gate behind the service's.

-- name: InsertOrderTransfer :one
INSERT INTO order_transfers (
    id, order_id, status, requested_by, from_customer_id, to_customer_id,
    from_email, token_hash, expires_at, closed_at
) VALUES (
    sqlc.arg('id'), sqlc.arg('order_id'), sqlc.arg('status'), sqlc.arg('requested_by'),
    sqlc.narg('from_customer_id'), sqlc.arg('to_customer_id'), sqlc.narg('from_email'),
    sqlc.narg('token_hash'), sqlc.narg('expires_at'),
    CASE WHEN sqlc.arg('status')::text = 'requested' THEN NULL ELSE now() END
)
RETURNING *;

-- name: GetOrderTransfer :one
SELECT * FROM order_transfers
WHERE id = $1;

-- LockOrderTransfer is taken after the order's lock, never before it.
-- name: LockOrderTransfer :one
SELECT * FROM order_transfers
WHERE id = $1
FOR UPDATE;

-- RequestedOrderTransfers are the order's requests still marked waiting,
-- expired ones included: the service reads the bound with its own clock.
-- name: RequestedOrderTransfers :many
SELECT * FROM order_transfers
WHERE order_id = $1 AND status = 'requested'
ORDER BY created_at DESC, id DESC;

-- name: CloseOrderTransfer :one
UPDATE order_transfers
SET status    = sqlc.arg('status'),
    closed_at = now()
WHERE id = sqlc.arg('id') AND status = 'requested'
RETURNING *;

-- name: ListOrderTransfers :many
SELECT * FROM order_transfers
WHERE order_id = sqlc.arg('order_id')
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit_count') OFFSET sqlc.arg('offset_count');

-- name: CountOrderTransfers :one
SELECT count(*) FROM order_transfers
WHERE order_id = $1;

-- MoveOrderCustomer hands the order and every order that adds to it to the
-- receiving customer and their address, while each still belongs to the
-- holder the transfer was written against. It returns the orders it moved.
-- name: MoveOrderCustomer :many
UPDATE orders
SET customer_id = sqlc.arg('to_customer_id'),
    email       = sqlc.arg('to_email'),
    updated_at  = now()
WHERE (id = sqlc.arg('order_id') OR adds_to_order_id = sqlc.arg('order_id'))
  AND personal_data_erased_at IS NULL
  AND customer_id IS NOT DISTINCT FROM sqlc.narg('from_customer_id')
RETURNING id;

-- EraseOrderTransferEmails empties the address a transfer asked, on the
-- orders an erasure forgets and wherever the person was the holder asked. It
-- is unconditional over what it matches, as the order's own anonymizing is,
-- so a second sweep writes the same rows; a holder matched by the address
-- alone is the exception a guest order's e-mail is.
-- name: EraseOrderTransferEmails :execrows
UPDATE order_transfers
SET from_email = NULL
WHERE order_id = ANY (sqlc.arg('order_ids')::text[])
   OR (sqlc.narg('customer_id')::text IS NOT NULL AND from_customer_id = sqlc.narg('customer_id')::text)
   OR (sqlc.narg('email')::text IS NOT NULL AND from_email = sqlc.narg('email')::text);

-- CancelOrderTransfersTo withdraws every request still waiting for the
-- person as its receiver: an erased customer receives no order (ADR 0459).
-- The status is the second gate, as in CloseOrderTransfer, so a request an
-- acceptance closed under its lock first stays accepted.
-- name: CancelOrderTransfersTo :execrows
UPDATE order_transfers
SET status    = 'canceled',
    closed_at = now()
WHERE to_customer_id = sqlc.arg('customer_id') AND status = 'requested';

-- OrderTransfersForDisclosure reads the transfers of the person's orders and
-- every transfer that names them, as holder or as receiver.
-- name: OrderTransfersForDisclosure :many
SELECT * FROM order_transfers
WHERE order_id = ANY (sqlc.arg('order_ids')::text[])
   OR (sqlc.narg('customer_id')::text IS NOT NULL
       AND (from_customer_id = sqlc.narg('customer_id')::text
            OR to_customer_id = sqlc.narg('customer_id')::text))
   OR (sqlc.narg('email')::text IS NOT NULL AND from_email = sqlc.narg('email')::text)
ORDER BY order_id, created_at, id;
