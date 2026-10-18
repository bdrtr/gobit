package models

import "time"

// An order changes hands through a transfer (ADR 0450).
//
// A shopper who can open the order at the storefront asks for it to be
// theirs, and the address the order is mailed at says yes or no with a token
// sent there; an operator moves an order at once. Accepting moves the order's
// customer and its e-mail to the receiver and withdraws its access tokens.

// TransferStatus is where a transfer stands.
type TransferStatus string

// The transfer statuses. A request leaves [TransferRequested] once.
const (
	// TransferRequested waits for the holder's answer.
	TransferRequested TransferStatus = "requested"
	// TransferAccepted moved the order.
	TransferAccepted TransferStatus = "accepted"
	// TransferDeclined was refused by the holder.
	TransferDeclined TransferStatus = "declined"
	// TransferCanceled was withdrawn by the shopper who asked, or closed by an
	// operator's move.
	TransferCanceled TransferStatus = "canceled"
	// TransferExpired is never stored: it is how a request still marked
	// waiting reads once its bound has passed.
	TransferExpired TransferStatus = "expired"
)

// TransferRequester says who wrote a transfer.
type TransferRequester string

// The two writers of a transfer.
const (
	// TransferByCustomer is a shopper's request at the storefront.
	TransferByCustomer TransferRequester = "customer"
	// TransferByOperator is an operator's move, accepted when written.
	TransferByOperator TransferRequester = "operator"
)

// TransferIDPrefix is the prefix of a transfer identifier.
const TransferIDPrefix = "otr_"

// NewTransferID produces a new transfer identifier.
func NewTransferID() string { return newID(TransferIDPrefix, time.Now()) }

// TransferTokenPrefix is the prefix of a transfer token, for leak scanners as
// [AccessTokenPrefix] is: a string starting with it is the answer to an
// order's transfer.
const TransferTokenPrefix = "ordxfr_"

// OrderTransfer is one transfer of an order.
type OrderTransfer struct {
	// ID is the identifier with the "otr_" prefix.
	ID string
	// OrderID is the order the transfer moves; an order that adds to it moves
	// with it.
	OrderID string
	// Status is the stored status; [OrderTransfer.StatusAt] reads expiry.
	Status TransferStatus
	// RequestedBy says whether a shopper asked or an operator moved.
	RequestedBy TransferRequester
	// FromCustomerID is the order's customer when the transfer was written;
	// empty for a guest's order.
	FromCustomerID string
	// ToCustomerID is the customer who receives the order.
	ToCustomerID string
	// FromEmail is the address the order held when the transfer was written,
	// the one a request asked; emptied by an erasure.
	FromEmail string
	// TokenHash is the digest of the token mailed to FromEmail; nil on an
	// operator's move.
	TokenHash []byte
	// ExpiresAt bounds a request; nil on an operator's move.
	ExpiresAt *time.Time
	// ClosedAt is when the transfer stopped waiting; nil while it waits.
	ClosedAt *time.Time
	// CreatedAt is when it was written, by the database clock.
	CreatedAt time.Time
}

// ExpiredAt reports whether a waiting request's bound has passed at now.
func (t OrderTransfer) ExpiredAt(now time.Time) bool {
	return t.Status == TransferRequested && t.ExpiresAt != nil && !now.Before(*t.ExpiresAt)
}

// StatusAt is the status a reader is told at now: the stored one, or
// [TransferExpired] for a request whose bound has passed.
func (t OrderTransfer) StatusAt(now time.Time) TransferStatus {
	if t.ExpiredAt(now) {
		return TransferExpired
	}

	return t.Status
}
