package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/b2b/models"
	"github.com/bdrtr/gobit/internal/modules/b2b/service"
)

// The DTOs are kept SEPARATE from the domain models: the JSON field names are
// the external contract and a rename made in the model must not break the
// client.

// companyDTO is the response body of a company.
type companyDTO struct {
	// ID is the company's identifier.
	ID string `json:"id"`
	// Name is the company's trade name.
	Name string `json:"name"`
	// Email is the company's contact address (normalized to lowercase).
	Email string `json:"email"`
	// Phone is the company's phone number.
	Phone string `json:"phone"`
	// Address is the street line of the billing address.
	Address string `json:"address"`
	// City is the city.
	City string `json:"city"`
	// PostalCode is the postal code.
	PostalCode string `json:"postal_code"`
	// CountryCode is the ISO 3166-1 alpha-2 country code (UPPERCASE); it may
	// be empty.
	CountryCode string `json:"country_code"`
	// CurrencyCode is the ISO 4217 currency code; spending limits are
	// expressed in this currency.
	CurrencyCode string `json:"currency_code"`
	// SpendingLimitResetPeriod is the reset interval of the employee limits:
	// "monthly", "yearly" or "never".
	SpendingLimitResetPeriod string `json:"spending_limit_reset_period"`
	// CreatedAt is the moment of creation (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment of the last update (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// employeeDTO is the response body of a company employee.
type employeeDTO struct {
	// ID is the identifier of the employee record.
	ID string `json:"id"`
	// CompanyID is the company the employee belongs to.
	CompanyID string `json:"company_id"`
	// CustomerID is the employee's customer record (customer module). The
	// value comes from the link layer; if it looks empty, the bond could not
	// be established.
	CustomerID string `json:"customer_id"`
	// SpendingLimit is the maximum amount that can be spent per window (minor
	// unit). null means UNLIMITED; 0 is a real zero limit.
	SpendingLimit *int64 `json:"spending_limit"`
	// IsCompanyAdmin reports whether the employee is a company administrator.
	IsCompanyAdmin bool `json:"is_company_admin"`
	// CreatedAt is the moment of creation.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment of the last update.
	UpdatedAt time.Time `json:"updated_at"`
}

// storeEmployeeDTO is the storefront customer's OWN employee record.
//
// It differs from the admin response by two fields, and both are derived from
// the company: the interval of the spending window and the start of that
// window. Those fields are ABSENT from the admin listing because reading the
// company per employee there would produce an N+1; in the storefront there is a
// single record anyway.
type storeEmployeeDTO struct {
	// ID is the identifier of the employee record.
	ID string `json:"id"`
	// CompanyID is the company the employee belongs to.
	CompanyID string `json:"company_id"`
	// CustomerID is the employee's customer record.
	CustomerID string `json:"customer_id"`
	// SpendingLimit is the maximum amount that can be spent per window (minor
	// unit); null means unlimited.
	SpendingLimit *int64 `json:"spending_limit"`
	// SpendingLimitResetPeriod is the reset interval of the limit (it comes
	// from the company).
	SpendingLimitResetPeriod string `json:"spending_limit_reset_period"`
	// SpendingWindowStart is the start of the current spending window; if the
	// period is "never" it is null (there is no window).
	//
	// THE REMAINING ALLOWANCE IS NOT HERE: computing the remainder requires
	// the sum of the orders inside the window and that data belongs to the
	// order module. A made-up remaining field would hand the client a wrong
	// number (see service.Membership).
	SpendingWindowStart *time.Time `json:"spending_window_start"`
	// IsCompanyAdmin reports whether the employee is a company administrator.
	IsCompanyAdmin bool `json:"is_company_admin"`
	// CreatedAt is the moment of creation.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment of the last update.
	UpdatedAt time.Time `json:"updated_at"`
}

// companyRequest is the company creation body.
type companyRequest struct {
	Name                     string `json:"name"`
	Email                    string `json:"email"`
	Phone                    string `json:"phone"`
	Address                  string `json:"address"`
	City                     string `json:"city"`
	PostalCode               string `json:"postal_code"`
	CountryCode              string `json:"country_code"`
	CurrencyCode             string `json:"currency_code"`
	SpendingLimitResetPeriod string `json:"spending_limit_reset_period"`
}

// updateCompanyRequest is the company update body.
//
// The fields are pointers: a field that is not given means "do not touch",
// while a given empty string is a real clear. In a body that did not separate
// the two cases, a client updating only its phone number would have erased the
// company's address.
type updateCompanyRequest struct {
	Name                     *string `json:"name"`
	Email                    *string `json:"email"`
	Phone                    *string `json:"phone"`
	Address                  *string `json:"address"`
	City                     *string `json:"city"`
	PostalCode               *string `json:"postal_code"`
	CountryCode              *string `json:"country_code"`
	CurrencyCode             *string `json:"currency_code"`
	SpendingLimitResetPeriod *string `json:"spending_limit_reset_period"`
}

// employeeRequest is the employee creation body.
type employeeRequest struct {
	CompanyID      string `json:"company_id"`
	CustomerID     string `json:"customer_id"`
	SpendingLimit  *int64 `json:"spending_limit"`
	IsCompanyAdmin bool   `json:"is_company_admin"`
}

// updateEmployeeRequest is the employee update body.
//
// REMOVING the limit is asked for with a separate flag. The reason is a limit
// of encoding/json: "spending_limit": null and the field not being sent at all
// resolve to the same nil pointer on the Go side, so "do not touch" cannot be
// separated from "make it unlimited". Had they not been separated, a limit set
// once could never be removed.
//
// The company and customer fields are ABSENT: both are the identity of the
// record and changing them means opening a new record, not updating this one
// (see service.UpdateEmployeeInput).
type updateEmployeeRequest struct {
	SpendingLimit      *int64 `json:"spending_limit"`
	ClearSpendingLimit bool   `json:"clear_spending_limit"`
	IsCompanyAdmin     *bool  `json:"is_company_admin"`
}

// toCompanyDTO converts the company into the response body.
func toCompanyDTO(c models.Company) companyDTO {
	return companyDTO{
		ID:                       c.ID,
		Name:                     c.Name,
		Email:                    c.Email,
		Phone:                    c.Phone,
		Address:                  c.Address,
		City:                     c.City,
		PostalCode:               c.PostalCode,
		CountryCode:              c.CountryCode,
		CurrencyCode:             c.CurrencyCode,
		SpendingLimitResetPeriod: string(c.SpendingLimitResetPeriod),
		CreatedAt:                c.CreatedAt,
		UpdatedAt:                c.UpdatedAt,
	}
}

// toEmployeeDTO converts the employee into the response body.
func toEmployeeDTO(e models.CompanyEmployee) employeeDTO {
	return employeeDTO{
		ID:             e.ID,
		CompanyID:      e.CompanyID,
		CustomerID:     e.CustomerID,
		SpendingLimit:  e.SpendingLimit,
		IsCompanyAdmin: e.IsCompanyAdmin,
		CreatedAt:      e.CreatedAt,
		UpdatedAt:      e.UpdatedAt,
	}
}

// toStoreEmployeeDTO converts the membership into the storefront response body.
func toStoreEmployeeDTO(m service.Membership) storeEmployeeDTO {
	return storeEmployeeDTO{
		ID:                       m.Employee.ID,
		CompanyID:                m.Employee.CompanyID,
		CustomerID:               m.Employee.CustomerID,
		SpendingLimit:            m.Employee.SpendingLimit,
		SpendingLimitResetPeriod: string(m.Company.SpendingLimitResetPeriod),
		SpendingWindowStart:      m.SpendingWindowStart,
		IsCompanyAdmin:           m.Employee.IsCompanyAdmin,
		CreatedAt:                m.Employee.CreatedAt,
		UpdatedAt:                m.Employee.UpdatedAt,
	}
}
