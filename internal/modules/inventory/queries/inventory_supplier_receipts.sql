-- inventory_supplier_receipts queries (ADR 0399).
--
-- A receipt is recorded by an operator, closed by receiving it — which writes the
-- counted units through the ledger — or by cancelling it, and never deleted. A
-- status leaves 'expected' once, so every write here is guarded by it.

-- name: CreateSupplierReceipt :one
INSERT INTO inventory_supplier_receipts (
    id, inventory_item_id, location_id, quantity, expected_at, reference
) VALUES (
    sqlc.arg('id')::text, sqlc.arg('inventory_item_id')::text, sqlc.arg('location_id')::text,
    sqlc.arg('quantity')::bigint, sqlc.arg('expected_at')::timestamptz, sqlc.narg('reference')::text
)
RETURNING *;

-- name: GetSupplierReceipt :one
SELECT * FROM inventory_supplier_receipts WHERE id = $1;

-- LockSupplierReceipt locks the receipt for a receive or a cancel; it comes
-- after the level in the lock order.
-- name: LockSupplierReceipt :one
SELECT * FROM inventory_supplier_receipts WHERE id = $1 FOR UPDATE;

-- ReceiveSupplierReceipt closes an expected receipt with the count and the
-- moment of the supplier_receipt movement that names it, so the record and the
-- ledger cannot disagree. No such movement, or a receipt no longer expected,
-- changes nothing.
-- name: ReceiveSupplierReceipt :execrows
UPDATE inventory_supplier_receipts r
SET status = 'received', received_quantity = m.delta, received_at = m.created_at,
    updated_at = m.created_at
FROM inventory_movements m
WHERE r.id = sqlc.arg('id')::text AND r.status = 'expected'
  AND m.reason = 'supplier_receipt' AND m.reference = r.id;

-- name: CancelSupplierReceipt :execrows
UPDATE inventory_supplier_receipts
SET status = 'canceled', canceled_at = clock_timestamp(), updated_at = clock_timestamp()
WHERE id = $1 AND status = 'expected';

-- ListSupplierReceipts pages one item's receipts by expected moment, every
-- status or one.
-- name: ListSupplierReceipts :many
SELECT * FROM inventory_supplier_receipts
WHERE inventory_item_id = sqlc.arg('inventory_item_id')::text
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY expected_at, id
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- name: CountSupplierReceipts :one
SELECT COUNT(*) FROM inventory_supplier_receipts
WHERE inventory_item_id = sqlc.arg('inventory_item_id')::text
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text);

-- CountExpectedSupplierReceipts is the item deletion's count, read under the
-- item's exclusive lock, which recording waits on.
-- name: CountExpectedSupplierReceipts :one
SELECT COUNT(*) FROM inventory_supplier_receipts
WHERE inventory_item_id = $1 AND status = 'expected';

-- CountExpectedSupplierReceiptsAtLocation is the close's count, read under the
-- location's exclusive lock, which recording waits on.
-- name: CountExpectedSupplierReceiptsAtLocation :one
SELECT COUNT(*) FROM inventory_supplier_receipts
WHERE location_id = $1 AND status = 'expected';

-- ExpectedSupplierReceiptsOfItems is the forecast's read: the items' receipts
-- still expected and not yet due, in the order they are expected. A receipt
-- whose moment has passed is left out, so the forecast errs late.
-- name: ExpectedSupplierReceiptsOfItems :many
SELECT * FROM inventory_supplier_receipts
WHERE inventory_item_id = ANY (sqlc.arg('item_ids')::text[])
  AND status = 'expected' AND expected_at > now()
ORDER BY inventory_item_id, expected_at, id;

-- WaitingBackordersOfItems is the forecast's view of the queue ADR 0392 fills:
-- the items' waiting claims in queue order, unlocked.
-- name: WaitingBackordersOfItems :many
SELECT * FROM inventory_backorders
WHERE inventory_item_id = ANY (sqlc.arg('item_ids')::text[]) AND status = 'waiting'
ORDER BY inventory_item_id, seq;
