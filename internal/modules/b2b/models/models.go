// Package models defines the domain models of the b2b module.
//
// The types here are FREE of database types: pgtype does not enter this
// package, the conversion is done in the repository wrapper. That way the
// service and API layers do not bind to a storage detail. Times are UTC, money
// is an INTEGER minor unit and deletion is SOFT.
package models

import (
	"strings"
	"time"
)

// Field length limits.
//
// The limits are not arbitrary: 320 characters for an e-mail is the upper bound
// of RFC 5321's local part (64) + "@" + domain name (255). The others are
// reasonable ceilings that keep a single request from writing unbounded text
// into the database, and they are enforced a second time by the CHECK
// constraints in the migration.
const (
	// MaxEmailLen is the maximum length of an e-mail address.
	MaxEmailLen = 320
	// MaxNameLen is the maximum length of short text fields such as a name.
	MaxNameLen = 255
	// MaxPhoneLen is the maximum length of a phone number.
	MaxPhoneLen = 32
	// MaxAddressLen is the maximum length of the address and city fields.
	MaxAddressLen = 255
	// MaxPostalCodeLen is the maximum length of the postal code.
	MaxPostalCodeLen = 32
)

// SpendingResetPeriod is the INTERVAL at which the spending limit is reset.
//
// # Which time window is meant
//
// This field is not a number on its own, it is a WINDOW DEFINITION: the
// employee's [CompanyEmployee.SpendingLimit] value is compared against the sum
// of the orders placed from the start of the window until now. The start of the
// window is computed with [SpendingResetPeriod.WindowStart] and follows the
// CALENDAR, not the creation date of the record: a monthly limit resets on the
// 1st of every month even if the company was opened on the 20th. The choice is
// deliberate — accounting periods run on the calendar, and a "month sliding
// with the company's opening day" would line up with no financial report.
//
// The window is UTC. Using a local time zone would mean that the month begins
// at different moments for two employees of the same company in two different
// countries.
//
// # WHO enforces the rule
//
// Not this module, the order module. The start of the window and the limit are
// published on the "b2b.interop" surface; the side that computes the spending
// (the sum of the orders placed inside the window) and compares it against the
// limit is the module that owns that sum. For the detail see
// internal/modules/b2b/service, Interop.SpendingLimitJSON.
type SpendingResetPeriod string

// The defined reset periods. The values are exactly the same as the CHECK
// constraint in the database (see migrations/000001_b2b_init.up.sql).
const (
	// ResetMonthly resets the limit on the 1st of every calendar month.
	ResetMonthly SpendingResetPeriod = "monthly"
	// ResetYearly resets the limit on 1 January of every calendar year.
	ResetYearly SpendingResetPeriod = "yearly"
	// ResetNever never resets the limit; the window is the employee's ENTIRE
	// history.
	ResetNever SpendingResetPeriod = "never"
)

// Valid reports whether the period is one of the defined ones.
//
// The type is a string and the caller can construct a value outside the enum;
// if such a value silently fell back to "never", a company that believed it had
// set a monthly limit would be left with a limit that never resets.
func (p SpendingResetPeriod) Valid() bool {
	return p == ResetMonthly || p == ResetYearly || p == ResetNever
}

// WindowStart returns the START moment of the current spending window.
//
// For [ResetNever] it returns nil: there is no window, the limit applies to the
// employee's entire history. For the other periods the returned moment is 00:00
// UTC on the first day of the current calendar month or year, and the window
// runs from that moment until NOW (start inclusive, upper end open).
//
// The function takes the time as a parameter; binding directly to time.Now
// would make the boundary moments (the first second of the month, the turn of
// the year) untestable.
func (p SpendingResetPeriod) WindowStart(now time.Time) *time.Time {
	utc := now.UTC()
	switch p {
	case ResetMonthly:
		start := time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
		return &start
	case ResetYearly:
		start := time.Date(utc.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
		return &start
	case ResetNever:
		return nil
	default:
		// An undefined value cannot enter the database (CHECK) and the
		// service rejects it; if we do land here, the safest behavior is not
		// to open a window at all — an "unbounded window" would mean
		// silently widening the limit.
		return nil
	}
}

// Company is the customer that shops on behalf of a LEGAL ENTITY rather than an
// individual.
//
// The company itself does not shop; [CompanyEmployee] records shop on its
// behalf. That is why there is NO spending limit on the company — the limit is
// per employee (see [CompanyEmployee.SpendingLimit]); the company only decides
// at which INTERVAL the limit is reset, because the reset period is the
// accounting period and cannot vary from employee to employee.
type Company struct {
	// ID is the time-ordered identifier with the "comp_" prefix.
	ID string
	// Name is the company's trade name; it is required.
	Name string
	// Email is the company's contact address; it is always stored normalized
	// to LOWERCASE. It is NOT UNIQUE (see the migration document).
	Email string
	// Phone is the company's phone number; it may be empty.
	Phone string
	// Address is the street line of the billing address; it may be empty.
	Address string
	// City is the city; it may be empty.
	City string
	// PostalCode is the postal code; it may be empty.
	PostalCode string
	// CountryCode is the ISO 3166-1 alpha-2 country code (UPPERCASE); it may
	// be empty. Whether the code really corresponds to an existing country is
	// NOT checked HERE; the owner of the country list is the region module.
	CountryCode string
	// CurrencyCode is the ISO 4217 currency code (UPPERCASE); it is required.
	// Spending limits are expressed in this currency.
	CurrencyCode string
	// SpendingLimitResetPeriod is the reset interval of the employee limits.
	SpendingLimitResetPeriod SpendingResetPeriod
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the soft delete moment; if nil the record is live.
	DeletedAt *time.Time
}

// CompanyEmployee is the employee who can shop on behalf of a company.
//
// The record is where B2B breaks the B2C assumption: the buyer is not an
// individual but an employee with a LIMITED SPENDING AUTHORITY. Their identity
// is still a customer — the cart, the order and the address live in the
// customer module; this record only says on behalf of which company and up to
// HOW MUCH that customer may shop.
type CompanyEmployee struct {
	// ID is the identifier with the "compemp_" prefix.
	ID string
	// CompanyID is the company the employee belongs to; it is an in-module
	// foreign key.
	CompanyID string
	// CustomerID is the employee's CUSTOMER record (customer module).
	//
	// CAUTION: this field has NO COLUMN in the database. The value is read
	// from the "b2b_employee_customer" link and filled in by the service
	// layer; the repository layer leaves it EMPTY. A column and a link
	// holding the same relation in two places would mean the two drifting
	// apart, and the drift would produce two different answers to the "my own
	// company" question in the storefront.
	CustomerID string
	// SpendingLimit is the maximum amount the employee may spend per window
	// (minor unit, in the company's currency).
	//
	// nil means UNLIMITED; 0 is a real zero limit and the employee cannot
	// spend at all. Collapsing the two into a single value would erase the
	// difference between "I set no limit" and "I set the limit to zero". For
	// the definition of the window see [SpendingResetPeriod].
	SpendingLimit *int64
	// IsCompanyAdmin reports whether the employee is a company administrator.
	IsCompanyAdmin bool
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the soft delete moment; if nil the record is live.
	DeletedAt *time.Time
}

// HasSpendingLimit reports whether the employee has a limited spending
// authority.
//
// An employee with a zero limit is LIMITED too: the check looks at nil, not at
// the value.
func (e CompanyEmployee) HasSpendingLimit() bool { return e.SpendingLimit != nil }

// NormalizeEmail converts the e-mail into its storage form: it is trimmed and
// lowered to LOWERCASE.
//
// The normalization is done on STORAGE, not on read: if "Muhasebe@X.com" and
// "muhasebe@x.com" are meant to denote the same address, both have to come down
// to the same bytes, otherwise the e-mail filter would take them for two
// different ones.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// NormalizeCountryCode converts the country code into its storage form: it is
// trimmed and raised to UPPERCASE. Validation belongs to the caller.
func NormalizeCountryCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// NormalizeCurrencyCode converts the currency code into its storage form: it is
// trimmed and raised to UPPERCASE. Validation belongs to the caller.
func NormalizeCurrencyCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}
