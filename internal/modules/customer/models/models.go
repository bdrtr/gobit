// Package models defines the domain models of the customer module.
//
// The types here are STRIPPED of database types: pgtype does not enter this
// package, the conversion is done in the repository wrapper. The service and
// API layers therefore do not bind to a storage detail. Times are UTC; deletion
// is SOFT.
package models

import (
	"strings"
	"time"
)

// Field length limits.
//
// The limits are not arbitrary: 320 characters for the e-mail is RFC 5321's
// upper bound for the local part (64) + "@" + the domain name (255). The others
// are reasonable ceilings that keep a single request from writing unbounded
// text into the database, and they are enforced a second time by the CHECK
// constraints in the migration.
const (
	// MaxEmailLen is the maximum length of an e-mail address.
	MaxEmailLen = 320
	// MaxNameLen is the maximum length of short text fields such as first name,
	// last name and company.
	MaxNameLen = 255
	// MaxPhoneLen is the maximum length of the phone number.
	MaxPhoneLen = 32
	// MaxAddressLen is the maximum length for the lines of the address.
	MaxAddressLen = 255
	// MaxPostalCodeLen is the maximum length of the postal code.
	MaxPostalCodeLen = 32
)

// Customer is a customer; it can be a guest as well as registered.
//
// # The guest and account distinction
//
// [Customer.HasAccount] is the ONE field that separates the two. The
// distinction's counterpart in the data model is e-mail uniqueness, and the
// decision is this:
//
//   - A REGISTERED account's e-mail is unique (partial unique index:
//     UNIQUE (email) WHERE has_account AND deleted_at IS NULL).
//   - GUEST records' e-mail is NOT unique; as many guest records as wanted can
//     be opened with the same e-mail.
//
// Rationale: these two requirements are true at the same time and cannot be
// expressed with a single full uniqueness constraint. A guest order is not an
// identity but the contact information of a one-off purchase; ordering a second
// time with the same address cannot be forbidden — had it been forbidden, the
// storefront would tell a customer who never opened an account that "this
// e-mail is in use", and the customer could not shop because of their own past
// order. A registered account, on the other hand, is an identity: the "log in
// with e-mail" arriving in Phase 8 MUST be single, because it could not choose
// between two matching records.
//
// The partial index expresses exactly this distinction and binds the rule to
// the database rather than to the application; for the conflict behavior of the
// guest-to-account transition see internal/modules/customer/service,
// ConvertGuestToAccount.
type Customer struct {
	// ID is the "cust_" prefixed, time-ordered id.
	ID string
	// Email is the customer's e-mail address; it is always stored normalized to
	// LOWER case (see [NormalizeEmail]).
	Email string
	// FirstName is the customer's first name; it can be empty.
	FirstName string
	// LastName is the customer's last name; it can be empty.
	LastName string
	// Phone is the customer's phone number; it can be empty.
	Phone string
	// HasAccount reports whether the record is an account or a guest record.
	HasAccount bool
	// Metadata is structural context the caller writes freely; it can be empty.
	Metadata map[string]any
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the soft delete moment; if nil the record is live.
	DeletedAt *time.Time
}

// IsGuest reports whether the record is a guest record.
func (c Customer) IsGuest() bool { return !c.HasAccount }

// CustomerGroup is a customer segment (e.g. "VIP", "B2B").
//
// The customer-group relation is MANY-TO-MANY: a customer can belong to several
// groups, a group can have several customers. The group's id corresponds to the
// "customer_group_id" attribute in the pricing module's rule context; there is
// NO compile-time or database bond between the two modules (Principle 2.2/2.4),
// the bond is established only in the computation context.
type CustomerGroup struct {
	// ID is the "custgrp_" prefixed id.
	ID string
	// Name is the group's display name; it is unique among live records.
	Name string
	// Metadata is structural context the caller writes freely; it can be empty.
	Metadata map[string]any
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the soft delete moment; if nil the record is live.
	DeletedAt *time.Time
}

// CustomerAddress is an address belonging to a customer.
//
// The default shipping and billing flags can be on AT MOST ONE address per
// customer; the constraint is enforced by the partial unique indexes in the
// database (see migrations/000001_customer_init.up.sql).
type CustomerAddress struct {
	// ID is the "addr_" prefixed id.
	ID string
	// CustomerID is the customer that owns the address.
	CustomerID string
	// FirstName is the first name on the address; it can be empty.
	FirstName string
	// LastName is the last name on the address; it can be empty.
	LastName string
	// Company is the company name; it can be empty.
	Company string
	// Address1 is the first line of the address; it is required.
	Address1 string
	// Address2 is the second line of the address; it can be empty.
	Address2 string
	// City is the city; it is required.
	City string
	// CountryCode is the ISO 3166-1 alpha-2 country code; it is always stored in
	// UPPER case.
	CountryCode string
	// PostalCode is the postal code; it can be empty.
	PostalCode string
	// Phone is the contact phone of the address; it can be empty.
	Phone string
	// IsDefaultShipping reports that the address is the default shipping address.
	IsDefaultShipping bool
	// IsDefaultBilling reports that the address is the default billing address.
	IsDefaultBilling bool
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the soft delete moment; if nil the record is live.
	DeletedAt *time.Time
}

// NormalizeEmail converts the e-mail into its storage form: it is trimmed and
// folded to LOWER case.
//
// Normalization is done ON STORAGE, not on read. The uniqueness index is over
// the raw column; if "Ali@X.com" and "ali@x.com" are to point at the same
// account, both of them have to come down to the same bytes. Normalizing at
// read time would not have kept two different spellings from entering the
// table.
//
// Folding the local part (before the @) to lower case can technically be
// considered contrary to the RFC — RFC 5321 leaves the local part
// case-sensitive — but in practice no provider uses that distinction, and
// leaving it sensitive would let the same customer open two accounts.
// Commercial correctness comes ahead of the letter of the standard here, and
// the decision is deliberate.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// NormalizeCountryCode converts the country code into its storage form: it is
// trimmed and raised to UPPER case. Validation belongs to the caller.
func NormalizeCountryCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}
