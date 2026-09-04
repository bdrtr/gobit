package models

import "time"

// DeliveryStatus is the status of a delivery log record.
type DeliveryStatus string

// Delivery statuses.
//
// The values are EXACTLY the same as the CHECK constraint in the migration; a
// typo here turns writing the record into a constraint violation.
const (
	// DeliveryPending means the record was opened and the provider has NOT
	// been reached yet.
	//
	// A row left permanently in this status is the proof of a FAULT: the send
	// happened but its result could not be written (or the process died in
	// between). Such a row cannot answer the question "did it go out?" and
	// has to be examined by hand.
	DeliveryPending DeliveryStatus = "pending"
	// DeliverySent means the provider accepted the notification.
	//
	// It does NOT mean it REACHED the customer: the provider contract (see
	// internal/core/provider) only reports that the request was accepted, the
	// delivery status is not queried.
	DeliverySent DeliveryStatus = "sent"
	// DeliveryFailed means the provider returned an error; the reason is in
	// the Error field.
	//
	// Nor does it mean the notification did NOT GO OUT: a request that ran
	// into a timeout may have been processed on the far side (the warning in
	// the core contract).
	DeliveryFailed DeliveryStatus = "failed"
	// DeliverySkipped means the provider was NOT reached at all, because
	// there was no address to send to.
	//
	// It is NOT an error: an order without an address (e.g. one opened by the
	// admin) is a valid record. Giving the status a name of its own separates
	// "there was no address" from "the provider refused" — the two call for
	// different fixes.
	DeliverySkipped DeliveryStatus = "skipped"
)

// String returns the textual form of the status.
func (s DeliveryStatus) String() string { return string(s) }

// Valid reports whether the status is a defined value.
func (s DeliveryStatus) Valid() bool {
	switch s {
	case DeliveryPending, DeliverySent, DeliveryFailed, DeliverySkipped:
		return true
	default:
		return false
	}
}

// Delivery is the log record of a single notification send attempt.
//
// # THERE IS NO RECIPIENT ADDRESS FIELD
//
// The record does NOT carry who it was sent to; the rationale is in the
// migration file (in short: the address already sits on the order, and a
// second copy raises the number of places that have to be erased). The field
// that ties the record to the order is [Delivery.Reference].
type Delivery struct {
	// ID is the identifier of the record.
	ID string
	// Template is the template of the notification sent (e.g. "order.placed").
	Template string
	// Channel is the send channel ("email" | "sms").
	Channel string
	// Reference is the identifier of the record the notification is bound to
	// (the order). It is free text, NOT a foreign key (Principle 2.2).
	Reference string
	// ProviderID is the identifier of the provider that performed the send.
	ProviderID string
	// Status is the outcome of the attempt.
	Status DeliveryStatus
	// Error is filled only while Status is [DeliveryFailed]; it is for
	// diagnosis.
	Error string
	// CreatedAt is the moment the record was opened, that is, the moment the
	// send was ATTEMPTED.
	CreatedAt time.Time
	// UpdatedAt is the moment the outcome was written.
	UpdatedAt time.Time
}

// DeliveryFilter holds the filter and pagination parameters of a delivery log
// listing.
//
// The pointer fields preserve the distinction between "not given" and "given
// empty": a nil Reference means the filter is not applied. Had a value type
// been used, the two cases could not be told apart.
type DeliveryFilter struct {
	// Reference, when given, returns only the records of that reference.
	Reference *string
	// Status, when given, returns only the records in that status.
	Status *string
	// Limit is the maximum number of rows to return.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
}
