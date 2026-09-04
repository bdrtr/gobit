package models

// CompanyFilter is the filter applied to the company listing.
//
// The fields are pointers: nil means "do not filter", while a non-nil pointer
// is a real filter even if its value is empty. In a design that did not
// separate the two cases, an empty filter value would silently turn into "list
// them all".
type CompanyFilter struct {
	// Email, if given, returns only the companies carrying this e-mail. The
	// value has to be normalized by the caller and MORE THAN ONE record may
	// come back: a company e-mail is not unique.
	Email *string
}

// EmployeeFilter is the filter applied to the employee listing.
type EmployeeFilter struct {
	// CompanyID, if given, returns only the employees of this company.
	CompanyID *string
	// IsCompanyAdmin, if given, filters on the admin / non-admin distinction.
	IsCompanyAdmin *bool
}

// CompanyPatch is the partial update of a company.
//
// A nil field means "do not touch", a non-nil field means "write this value";
// an empty string is a real clear on the address fields (the old postal code of
// a company that has moved has to be removable).
type CompanyPatch struct {
	// Name is the company's new trade name; if given it cannot be empty.
	Name *string
	// Email is the new e-mail; it has to be normalized by the caller.
	Email *string
	// Phone is the new phone number.
	Phone *string
	// Address is the new street line of the address.
	Address *string
	// City is the new city.
	City *string
	// PostalCode is the new postal code.
	PostalCode *string
	// CountryCode is the new country code; it has to be normalized by the
	// caller.
	CountryCode *string
	// CurrencyCode is the new currency code; if given it cannot be empty.
	CurrencyCode *string
	// SpendingLimitResetPeriod is the new reset interval.
	SpendingLimitResetPeriod *SpendingResetPeriod
}

// EmployeePatch is the partial update of an employee.
//
// CHANGING THE COMPANY IS DELIBERATELY ABSENT: if an employee's company
// changes, that is not an update of the same record but the closing of the old
// record and the opening of a new one — the spending history belongs to the old
// company, and moving the record would silently hand that history over to the
// new one.
type EmployeePatch struct {
	// SpendingLimit is the new spending limit (minor unit).
	//
	// nil means "do not touch"; to pull the limit up to UNLIMITED,
	// [EmployeePatch.ClearSpendingLimit] is used. Two fields are needed
	// because the field itself can be nil as well and a single pointer cannot
	// separate "do not touch" from "make it unlimited".
	SpendingLimit *int64
	// ClearSpendingLimit, if true, removes the limit (the employee becomes
	// unlimited). Giving it together with SpendingLimit is meaningless; the
	// service rejects it.
	ClearSpendingLimit bool
	// IsCompanyAdmin is the new value of the admin flag.
	IsCompanyAdmin *bool
}
