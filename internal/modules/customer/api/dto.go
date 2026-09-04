package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// The DTOs are kept SEPARATE from the domain models: JSON field names are the
// external contract, and a rename made in the model must not break the client.

// customerDTO is the response body of a customer.
type customerDTO struct {
	// ID is the customer's id.
	ID string `json:"id"`
	// Email is the customer's e-mail (normalized to lower case).
	Email string `json:"email"`
	// FirstName is the customer's first name.
	FirstName string `json:"first_name"`
	// LastName is the customer's last name.
	LastName string `json:"last_name"`
	// Phone is the customer's phone.
	Phone string `json:"phone"`
	// HasAccount reports whether the record is a registered account or a guest.
	HasAccount bool `json:"has_account"`
	// Metadata is free structural context; if empty it does not appear in the
	// body.
	Metadata map[string]any `json:"metadata,omitempty"`
	// CreatedAt is the moment of creation (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment of the last update (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// customerGroupDTO is the response body of a customer group.
type customerGroupDTO struct {
	// ID is the group's id.
	ID string `json:"id"`
	// Name is the group's name.
	Name string `json:"name"`
	// Metadata is free structural context; if empty it does not appear in the
	// body.
	Metadata map[string]any `json:"metadata,omitempty"`
	// CreatedAt is the moment of creation.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment of the last update.
	UpdatedAt time.Time `json:"updated_at"`
}

// addressDTO is the response body of a customer address.
type addressDTO struct {
	// ID is the address's id.
	ID string `json:"id"`
	// CustomerID is the customer that owns the address.
	CustomerID string `json:"customer_id"`
	// FirstName is the first name on the address.
	FirstName string `json:"first_name"`
	// LastName is the last name on the address.
	LastName string `json:"last_name"`
	// Company is the company name.
	Company string `json:"company"`
	// Address1 is the first line of the address.
	Address1 string `json:"address_1"`
	// Address2 is the second line of the address.
	Address2 string `json:"address_2"`
	// City is the city.
	City string `json:"city"`
	// CountryCode is the ISO 3166-1 alpha-2 country code (UPPER case).
	CountryCode string `json:"country_code"`
	// PostalCode is the postal code.
	PostalCode string `json:"postal_code"`
	// Phone is the contact phone.
	Phone string `json:"phone"`
	// IsDefaultShipping is the default shipping address flag.
	IsDefaultShipping bool `json:"is_default_shipping"`
	// IsDefaultBilling is the default billing address flag.
	IsDefaultBilling bool `json:"is_default_billing"`
	// CreatedAt is the moment of creation.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment of the last update.
	UpdatedAt time.Time `json:"updated_at"`
}

// customerRequest is the body of customer creation and of guest registration.
type customerRequest struct {
	Email     string         `json:"email"`
	FirstName string         `json:"first_name"`
	LastName  string         `json:"last_name"`
	Phone     string         `json:"phone"`
	Metadata  map[string]any `json:"metadata"`
}

// updateCustomerRequest is the customer update body.
//
// The fields are pointers: a field that is not given means "do not touch",
// while a given empty string means a real clearing. In a body that does not
// separate the two cases, a client that does not send its name would have
// deleted its name.
type updateCustomerRequest struct {
	Email     *string        `json:"email"`
	FirstName *string        `json:"first_name"`
	LastName  *string        `json:"last_name"`
	Phone     *string        `json:"phone"`
	Metadata  map[string]any `json:"metadata"`
}

// groupRequest is the customer group creation body.
type groupRequest struct {
	Name     string         `json:"name"`
	Metadata map[string]any `json:"metadata"`
}

// updateGroupRequest is the customer group update body.
//
// The name is a pointer: a name that is not given means "do not touch". Because
// the name is a required field it cannot be empty when it is given; in a body
// that does not separate the two cases, a client sending only metadata would
// have deleted the group's name.
type updateGroupRequest struct {
	Name     *string        `json:"name"`
	Metadata map[string]any `json:"metadata"`
}

// groupMemberRequest is the body of adding a customer to a group.
type groupMemberRequest struct {
	CustomerID string `json:"customer_id"`
}

// addressRequest is the address creation body.
type addressRequest struct {
	FirstName         string `json:"first_name"`
	LastName          string `json:"last_name"`
	Company           string `json:"company"`
	Address1          string `json:"address_1"`
	Address2          string `json:"address_2"`
	City              string `json:"city"`
	CountryCode       string `json:"country_code"`
	PostalCode        string `json:"postal_code"`
	Phone             string `json:"phone"`
	IsDefaultShipping bool   `json:"is_default_shipping"`
	IsDefaultBilling  bool   `json:"is_default_billing"`
}

// updateAddressRequest is the address update body.
//
// The default flags are DELIBERATELY absent: because changing a flag concerns
// the customer's other addresses as well, it is done through separate
// endpoints.
type updateAddressRequest struct {
	FirstName   *string `json:"first_name"`
	LastName    *string `json:"last_name"`
	Company     *string `json:"company"`
	Address1    *string `json:"address_1"`
	Address2    *string `json:"address_2"`
	City        *string `json:"city"`
	CountryCode *string `json:"country_code"`
	PostalCode  *string `json:"postal_code"`
	Phone       *string `json:"phone"`
}

// toCustomerDTO converts the customer into the response body.
func toCustomerDTO(c models.Customer) customerDTO {
	return customerDTO{
		ID:         c.ID,
		Email:      c.Email,
		FirstName:  c.FirstName,
		LastName:   c.LastName,
		Phone:      c.Phone,
		HasAccount: c.HasAccount,
		Metadata:   c.Metadata,
		CreatedAt:  c.CreatedAt,
		UpdatedAt:  c.UpdatedAt,
	}
}

// toGroupDTO converts the group into the response body.
func toGroupDTO(g models.CustomerGroup) customerGroupDTO {
	return customerGroupDTO{
		ID:        g.ID,
		Name:      g.Name,
		Metadata:  g.Metadata,
		CreatedAt: g.CreatedAt,
		UpdatedAt: g.UpdatedAt,
	}
}

// toAddressDTO converts the address into the response body.
func toAddressDTO(a models.CustomerAddress) addressDTO {
	return addressDTO{
		ID:                a.ID,
		CustomerID:        a.CustomerID,
		FirstName:         a.FirstName,
		LastName:          a.LastName,
		Company:           a.Company,
		Address1:          a.Address1,
		Address2:          a.Address2,
		City:              a.City,
		CountryCode:       a.CountryCode,
		PostalCode:        a.PostalCode,
		Phone:             a.Phone,
		IsDefaultShipping: a.IsDefaultShipping,
		IsDefaultBilling:  a.IsDefaultBilling,
		CreatedAt:         a.CreatedAt,
		UpdatedAt:         a.UpdatedAt,
	}
}
