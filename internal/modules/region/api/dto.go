package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/region/models"
	"github.com/bdrtr/gobit/internal/modules/region/service"
)

// The DTOs are kept SEPARATE from the domain models: the JSON field names are
// the external contract and a rename made in the model must not break a client.

// regionDTO is the response body of a region.
type regionDTO struct {
	// ID is the region's id.
	ID string `json:"id"`
	// Name is the region's display name.
	Name string `json:"name"`
	// CurrencyCode is the region's currency (ISO 4217, UPPER case).
	CurrencyCode string `json:"currency_code"`
	// AutomaticTaxes states whether the tax is applied automatically.
	AutomaticTaxes bool `json:"automatic_taxes"`
	// TaxRateBps is the region's FALLBACK tax rate; it is in BASIS POINTS
	// (2000 = 20%).
	//
	// The unit at the end of the field name is deliberate: a body of
	// "tax_rate": 20 would stay unclear about whether it is 20% or 0.2 and
	// would produce a hundredfold error on the client side. A basis point is an
	// integer, it is not a float (plan Section 8).
	TaxRateBps int32 `json:"tax_rate_bps"`
	// CreatedAt is the moment of creation (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment of the last update (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// storeRegionDTO is the body of a region that goes back TO THE CUSTOMER.
//
// Its difference from the admin body is deliberate: the storefront has to see
// the currency's symbol and its decimal digits (amounts are minor unit
// integers), but the tax rate and the automatic tax flag are business
// configuration and do not go to the customer — the tax appears already
// computed inside the cart total.
type storeRegionDTO struct {
	// ID is the region's id.
	ID string `json:"id"`
	// Name is the region's display name.
	Name string `json:"name"`
	// CurrencyCode is the region's currency.
	CurrencyCode string `json:"currency_code"`
	// Currency is the currency's presentation information; null if it is not found.
	Currency *currencyDTO `json:"currency"`
	// Countries are the region's countries; an empty slice if they were not requested.
	Countries []countryDTO `json:"countries"`
}

// currencyDTO is the response body of a currency.
type currencyDTO struct {
	// Code is the ISO 4217 code (UPPER case).
	Code string `json:"code"`
	// Symbol is the display symbol.
	Symbol string `json:"symbol"`
	// Name is the currency's English name in ISO.
	Name string `json:"name"`
	// DecimalDigits is the number of decimal digits (TRY/USD 2, JPY 0, KWD 3).
	//
	// Amounts are carried as minor unit INTEGERS; the presentation layer learns
	// the division factor (10^DecimalDigits) from here. A client assuming a
	// fixed 100 shows yen amounts a hundred times too small.
	DecimalDigits int32 `json:"decimal_digits"`
}

// countryDTO is the response body of a country.
type countryDTO struct {
	// Code is the ISO 3166-1 alpha-2 code (UPPER case).
	Code string `json:"code"`
	// Name is the country's English short name in ISO.
	Name string `json:"name"`
	// RegionID is the region the country is attached to; null if it is not attached.
	RegionID *string `json:"region_id"`
}

// createRegionRequest is the region creation request.
type createRegionRequest struct {
	// Name is the region's display name; it is required.
	Name string `json:"name"`
	// CurrencyCode is the ISO 4217 code; upper/lower case is free. It is required.
	CurrencyCode string `json:"currency_code"`
	// AutomaticTaxes states whether the tax is applied automatically.
	AutomaticTaxes bool `json:"automatic_taxes"`
	// TaxRateBps is the region's FALLBACK tax rate (basis points; 2000 = 20%).
	TaxRateBps int32 `json:"tax_rate_bps"`
}

// updateRegionRequest is the region update request.
//
// All the fields are pointers: a field that is not given DOES NOT CHANGE. Had
// the whole body been demanded, a client that forgets to send tax_rate_bps in
// its body would silently zero the rate.
type updateRegionRequest struct {
	// Name is the new name; if null/missing the name does not change.
	Name *string `json:"name"`
	// CurrencyCode is the new currency code; if null/missing it does not change.
	CurrencyCode *string `json:"currency_code"`
	// AutomaticTaxes states whether the tax is applied automatically; if
	// null/missing it does not change.
	AutomaticTaxes *bool `json:"automatic_taxes"`
	// TaxRateBps is the new tax rate (basis points); if null/missing it does not change.
	TaxRateBps *int32 `json:"tax_rate_bps"`
}

// addCountryRequest is the request that adds a country to a region.
type addCountryRequest struct {
	// CountryCode is the ISO 3166-1 alpha-2 code; upper/lower case is free.
	CountryCode string `json:"country_code"`
}

// toRegionDTO converts the region model into the admin response body.
func toRegionDTO(region models.Region) regionDTO {
	return regionDTO{
		ID:             region.ID,
		Name:           region.Name,
		CurrencyCode:   region.CurrencyCode,
		AutomaticTaxes: region.AutomaticTaxes,
		TaxRateBps:     region.TaxRate,
		CreatedAt:      region.CreatedAt,
		UpdatedAt:      region.UpdatedAt,
	}
}

// toCurrencyDTO converts the currency model into the response body.
func toCurrencyDTO(currency models.Currency) currencyDTO {
	return currencyDTO{
		Code:          currency.Code,
		Symbol:        currency.Symbol,
		Name:          currency.Name,
		DecimalDigits: currency.DecimalDigits,
	}
}

// toCountryDTO converts the country model into the response body.
func toCountryDTO(country models.Country) countryDTO {
	return countryDTO{
		Code:     country.Code,
		Name:     country.Name,
		RegionID: country.RegionID,
	}
}

// toCreateRegionInput converts the request body into the service input.
//
// NO validation is done here: the service decides on validity, and there being
// a single validation site makes sure that HTTP and a call between modules see
// the same rules.
func toCreateRegionInput(req createRegionRequest) service.CreateRegionInput {
	return service.CreateRegionInput{
		Name:           req.Name,
		CurrencyCode:   req.CurrencyCode,
		AutomaticTaxes: req.AutomaticTaxes,
		TaxRate:        req.TaxRateBps,
	}
}

// toUpdateRegionInput converts the request body into the service input.
func toUpdateRegionInput(req updateRegionRequest) service.UpdateRegionInput {
	return service.UpdateRegionInput{
		Name:           req.Name,
		CurrencyCode:   req.CurrencyCode,
		AutomaticTaxes: req.AutomaticTaxes,
		TaxRate:        req.TaxRateBps,
	}
}
