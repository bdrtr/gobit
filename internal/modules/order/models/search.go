package models

// OrderSearch is what the order list's q found to look for (ADR 0456).
//
// The two fields are united, not intersected: an order is a hit by its
// number, or by its words. "#1042" names the number alone, "1042" both, and
// any other q the words alone.
type OrderSearch struct {
	// DisplayID, when given, is an order number the hit may carry.
	DisplayID *int64
	// Text, when given, is matched against the order's e-mail and the
	// addresses it holds now, folded and split in the database.
	Text *string
}

// PaymentStatus is where an order's payment stands, read from what its
// summary reports collected and refunded against its total less its credits
// (ADR 0457). The words are the payment collection's where they mean the
// same; an authorized payment not yet captured is [PaymentNotPaid] here,
// because the summary reports money moved and not a promise of it.
type PaymentStatus string

const (
	// PaymentNotPaid is an order nothing was collected for while it owes.
	PaymentNotPaid PaymentStatus = "not_paid"
	// PaymentPartiallyCaptured is an order collected below what it owes.
	PaymentPartiallyCaptured PaymentStatus = "partially_captured"
	// PaymentCaptured is an order collected up to what it owes, nothing
	// refunded; an order owing nothing is captured.
	PaymentCaptured PaymentStatus = "captured"
	// PaymentPartiallyRefunded is an order refunded less than was collected.
	PaymentPartiallyRefunded PaymentStatus = "partially_refunded"
	// PaymentRefunded is an order refunded all that was collected.
	PaymentRefunded PaymentStatus = "refunded"
)

// Valid reports whether the status is one of the five.
func (s PaymentStatus) Valid() bool {
	switch s {
	case PaymentNotPaid, PaymentPartiallyCaptured, PaymentCaptured, PaymentPartiallyRefunded, PaymentRefunded:
		return true
	default:
		return false
	}
}
