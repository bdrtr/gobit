package models

import "github.com/bdrtr/gobit/internal/core/page"

// CustomerFilter is the filter applied to the customer listing.
//
// All of the fields are pointers: nil means "do not filter", while a non-nil
// pointer is a real filter even when its value is the zero value (empty string,
// false). In a design that does not separate the two cases, a "list the ones
// without an account" request would silently turn into "list them all".
type CustomerFilter struct {
	// Email, when given, returns only the customers holding this e-mail address.
	// The value must have been normalized by the caller.
	Email *string
	// HasAccount, when given, filters by the guest/registered distinction.
	HasAccount *bool
	// GroupID, when given, returns only the members of this group.
	GroupID *string
	// After is the keyset position the page starts below; the zero value is the
	// first page.
	//
	// It is applied TOGETHER with the offset so the query keeps one shape; the
	// API refuses the two at once, because they name two different positions.
	After page.Cursor
}

// CustomerPatch is the partial update of a customer.
//
// A nil field means "do not touch", a non-nil field means "write this value";
// an empty string is a real clearing. nil carries the same meaning for
// Metadata: if a map is given, the WHOLE column is replaced, no merging is
// done.
type CustomerPatch struct {
	// Email is the new e-mail; it must have been normalized by the caller.
	Email *string
	// FirstName is the new first name.
	FirstName *string
	// LastName is the new last name.
	LastName *string
	// Phone is the new phone.
	Phone *string
	// Metadata is the new metadata map; it replaces the whole column.
	Metadata map[string]any
}

// CustomerGroupPatch is the partial update of a customer group.
//
// A nil field means "do not touch", a non-nil field means "write this value".
// If a Metadata map is given, the WHOLE column is replaced, no merging is done.
type CustomerGroupPatch struct {
	// Name is the group's new name; it must have been trimmed by the caller and
	// is unique among live groups.
	Name *string
	// Metadata is the new metadata map; it replaces the whole column.
	Metadata map[string]any
}

// AddressPatch is the partial update of an address.
//
// The default shipping/billing flags are DELIBERATELY absent here: changing a
// flag is an operation that concerns the customer's other addresses as well and
// cannot be done with a single-row update (see SetDefaultAddress).
type AddressPatch struct {
	// FirstName is the new first name.
	FirstName *string
	// LastName is the new last name.
	LastName *string
	// Company is the new company name.
	Company *string
	// Address1 is the new first line of the address.
	Address1 *string
	// Address2 is the new second line of the address.
	Address2 *string
	// City is the new city.
	City *string
	// CountryCode is the new country code; it must be normalized by the caller.
	CountryCode *string
	// PostalCode is the new postal code.
	PostalCode *string
	// Phone is the new phone.
	Phone *string
}

// DefaultKind is which kind of default an address is to be marked as.
type DefaultKind uint8

// The kinds of the default address.
const (
	// DefaultShipping is the default shipping address (the zero value).
	DefaultShipping DefaultKind = iota
	// DefaultBilling is the default billing address.
	DefaultBilling
)

// String returns the readable name of the kind.
func (k DefaultKind) String() string {
	switch k {
	case DefaultBilling:
		return "billing"
	case DefaultShipping:
		return "shipping"
	default:
		return "shipping"
	}
}

// Valid reports whether the kind is defined.
//
// The type is exported and a caller can construct a value outside the enum; if
// such a value silently fell through to shipping, the client would be changing
// the shipping address while believing it had marked the billing address.
func (k DefaultKind) Valid() bool {
	return k == DefaultShipping || k == DefaultBilling
}
