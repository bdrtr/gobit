package models

import "time"

// SupplierReceiptStatus is where an expected supplier receipt stands.
type SupplierReceiptStatus string

// The three states of a receipt. A receipt starts expected and ends received or
// canceled; neither end is left (ADR 0399).
const (
	// SupplierReceiptExpected is a receipt whose units are still owed.
	SupplierReceiptExpected SupplierReceiptStatus = "expected"
	// SupplierReceiptReceived is a receipt whose counted units were written
	// through the ledger as a [MovementSupplierReceipt].
	SupplierReceiptReceived SupplierReceiptStatus = "received"
	// SupplierReceiptCanceled is a receipt that will bring nothing.
	SupplierReceiptCanceled SupplierReceiptStatus = "canceled"
)

// Valid reports whether the status is a defined value.
func (s SupplierReceiptStatus) Valid() bool {
	switch s {
	case SupplierReceiptExpected, SupplierReceiptReceived, SupplierReceiptCanceled:
		return true
	default:
		return false
	}
}

// String returns the text representation of the status.
func (s SupplierReceiptStatus) String() string { return string(s) }

// SupplierReceipt is an expected supplier receipt: Quantity units of the item a
// supplier owes the location, expected to be sellable there at ExpectedAt
// (ADR 0399).
type SupplierReceipt struct {
	// ID is the "invsup_" prefixed, time-sortable id.
	ID string
	// InventoryItemID and LocationID are the item owed and the warehouse it is
	// owed to.
	InventoryItemID string
	LocationID      string
	// Quantity is the units expected; it is always positive.
	Quantity int64
	// ExpectedAt is when the units are expected to be SELLABLE at the
	// warehouse, after put-away.
	ExpectedAt time.Time
	// Reference is the embedder's own document number; empty when none was
	// given. gobit keeps no supplier, price or purchase order.
	Reference string
	// Status is where the receipt stands.
	Status SupplierReceiptStatus
	// ReceivedQuantity and ReceivedAt are the delta and the moment of the
	// supplier_receipt movement that closed the receipt; zero until then.
	ReceivedQuantity int64
	ReceivedAt       *time.Time
	// CanceledAt is when the receipt was canceled; nil otherwise.
	CanceledAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// SupplierReceiptFilter narrows and pages an item's receipts.
type SupplierReceiptFilter struct {
	InventoryItemID string
	// Status is one status, or empty for every one.
	Status SupplierReceiptStatus
	Limit  int64
	Offset int64
}
