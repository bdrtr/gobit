package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/settings/models"
)

// storeProfileRequest is the body of the write.
//
// Every field is sent on every write, because the record is replaced rather than
// patched: the reason is on [API.setStoreProfile].
type storeProfileRequest struct {
	// LegalName is the name documents are issued under; it is REQUIRED.
	LegalName string `json:"legal_name"`
	// TaxNumber is the VKN/TCKN or its equivalent; it may be left out.
	TaxNumber string `json:"tax_number"`
	// TaxOffice is the office that number belongs to; it may be left out.
	TaxOffice string `json:"tax_office"`
	// Email is the shop's own address as printed; it may be left out.
	Email string `json:"email"`
	// Address is the printed address, already formatted into lines.
	Address string `json:"address"`
	// CountryCode is the ISO 3166-1 alpha-2 country; it is REQUIRED.
	CountryCode string `json:"country_code"`
}

// storeProfileDTO is the shop's identity as the endpoint answers it.
type storeProfileDTO struct {
	LegalName   string    `json:"legal_name"`
	TaxNumber   string    `json:"tax_number"`
	TaxOffice   string    `json:"tax_office"`
	Email       string    `json:"email"`
	Address     string    `json:"address"`
	CountryCode string    `json:"country_code"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// toProfileDTO converts the model into the response body.
//
// The empty fields are sent as empty strings rather than omitted: a client
// rendering a settings form needs to know the field exists and is blank, which
// is a different thing from a field the server does not have.
func toProfileDTO(profile models.StoreProfile) storeProfileDTO {
	return storeProfileDTO{
		LegalName:   profile.LegalName,
		TaxNumber:   profile.TaxNumber,
		TaxOffice:   profile.TaxOffice,
		Email:       profile.Email,
		Address:     profile.Address,
		CountryCode: profile.CountryCode,
		CreatedAt:   profile.CreatedAt,
		UpdatedAt:   profile.UpdatedAt,
	}
}
