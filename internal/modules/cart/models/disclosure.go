package models

import "time"

// The types in this file carry the rows a DISCLOSURE reads — the answer to
// "what does this module hold about this person" (ADR 0029, ADR 0033).
//
// # Why they are not [Cart] and [CartAddress]
//
// Every other read in this module returns the whole row, and reusing those
// types here was the obvious first move. It was rejected because the disclosure
// statements deliberately select ONLY the columns the module declared as
// personal: a [Cart] returned from such a query would carry an empty
// CurrencyCode, a zero Total and a zero RegionID, and nothing in the type would
// say whether the cart really has none or whether the query never asked. A
// caller cannot tell those apart, and this is the one path in the module where
// being wrong about what is held is the whole failure.
//
// The narrow types make the omission a property of the TYPE. What is not here
// was not read, and what is here is exactly what the declaration names — plus
// the identifiers that tie a row to its cart, because a dossier a person reads
// is grouped by session rather than by table.

// PersonalCart is the carts row as a disclosure reads it.
//
// [PersonalCart.CreatedAt] is not personal data and is not reported as a
// holding. It is here for one sentence: when the bound on how many carts one
// answer carries is hit, the answer says which moment it cut at, and a
// truncation the person can date is one they can ask about.
type PersonalCart struct {
	// ID is the cart's identifier; it names the record in the dossier.
	ID string
	// CustomerID is the customer module's identifier for the shopper; empty on
	// a guest cart.
	CustomerID string
	// Email is the address on the cart; empty once an erasure has run.
	Email string
	// Metadata is the caller's free-form data on the cart. gobit does not look
	// inside it and does not judge whether it holds a person.
	Metadata map[string]any
	// CreatedAt is when the cart was opened, UTC.
	CreatedAt time.Time
}

// PersonalAddress is the cart_addresses row as a disclosure reads it.
//
// Every column the declaration names is here, country_code and metadata
// included — the two the erasure deliberately leaves behind. What is still held
// after an anonymization is still held, and a disclosure that skipped it would
// contradict the erasure's own report, which lists both as kept.
//
// address_type is NOT here. It says what the address was for rather than who it
// belongs to, it is not declared, and a value that is not declared may not
// appear in a record.
type PersonalAddress struct {
	// ID is the address row's identifier and CartID is the cart it hangs off.
	ID     string
	CartID string
	// SourceAddressID is the entry of the shopper's address book this copy was
	// taken from.
	SourceAddressID string
	// The name, company and location columns, exactly as the declaration names
	// them; every one of them is optional in the schema and an empty value
	// means the column is NULL.
	FirstName   string
	LastName    string
	Company     string
	Address1    string
	Address2    string
	City        string
	Province    string
	PostalCode  string
	CountryCode string
	Phone       string
	// Metadata is the caller's free-form data on the address; delivery
	// instructions are typed here.
	Metadata map[string]any
}

// PersonalNote is one row whose ONLY declared column is a free-form one.
//
// It serves both cart_line_items.metadata and cart_shipping_methods.data, and
// one type covers the two because the shape of the answer is identical: a row
// id, the cart it belongs to, and a document gobit never inspects. The column's
// NAME differs between the two tables and is not carried here — the service
// takes it from the declaration, which is the only place that may decide what a
// disclosed column is called.
type PersonalNote struct {
	// ID is the row's identifier and CartID is the cart it hangs off.
	ID     string
	CartID string
	// Data is the free-form document the row holds.
	Data map[string]any
}
