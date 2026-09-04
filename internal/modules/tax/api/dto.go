package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// The DTOs are kept SEPARATE from the domain models: JSON field names are an
// external contract and a rename done in the model must not break the client.

// taxRegionDTO is the response body of a tax region.
type taxRegionDTO struct {
	// ID is the region's id.
	ID string `json:"id"`
	// CountryCode is the ISO 3166-1 alpha-2 code (UPPER case).
	CountryCode string `json:"country_code"`
	// ProvinceCode is the province/state code; null on a root region.
	ProvinceCode *string `json:"province_code"`
	// ParentID is the id of the root region; null on a root region.
	ParentID *string `json:"parent_id"`
	// ProviderID is the tax provider's id. When empty, a province region
	// inherits the country's provider and a root region applies local
	// calculation.
	ProviderID string `json:"provider_id"`
	// Metadata is free-form metadata; when empty the field does not appear.
	Metadata map[string]any `json:"metadata,omitempty"`
	// CreatedAt is the creation instant (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the last update instant (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// taxRateDTO is the response body of a tax rate.
type taxRateDTO struct {
	// ID is the rate's id.
	ID string `json:"id"`
	// TaxRegionID is the region the rate belongs to.
	TaxRegionID string `json:"tax_region_id"`
	// Name is the rate's display name.
	Name string `json:"name"`
	// Code is the reconciliation code; null when not given.
	Code *string `json:"code"`
	// RateBps is the rate; it is in BASIS POINTS (2000 = 20%).
	//
	// The unit at the end of the field name is deliberate: whether a "rate": 20
	// body were 20% or 0.2 would stay ambiguous and would produce a hundredfold
	// error on the client side. A basis point is an integer, not a float (plan
	// Section 8).
	RateBps int32 `json:"rate_bps"`
	// IsDefault is whether this is the region's default rate.
	IsDefault bool `json:"is_default"`
	// Metadata is free-form metadata; when empty the field does not appear.
	Metadata map[string]any `json:"metadata,omitempty"`
	// CreatedAt is the creation instant (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the last update instant (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// taxRateRuleDTO is the response body of a tax rule.
type taxRateRuleDTO struct {
	// ID is the rule's id.
	ID string `json:"id"`
	// TaxRateID is the rate the rule is attached to.
	TaxRateID string `json:"tax_rate_id"`
	// Reference is the kind of the item: "product", "product_type" or
	// "shipping_option".
	Reference string `json:"reference"`
	// ReferenceID is the id within that kind; it belongs to ANOTHER module and
	// this module does not verify its existence.
	ReferenceID string `json:"reference_id"`
	// CreatedAt is the creation instant (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
}

// createTaxRegionRequest is the tax region creation request.
type createTaxRegionRequest struct {
	// CountryCode is the ISO 3166-1 alpha-2 code; case is free. It is
	// required.
	CountryCode string `json:"country_code"`
	// ProvinceCode is the province/state code. Left empty, a COUNTRY ROOT is
	// created; given a value, parent_id is required too.
	ProvinceCode string `json:"province_code"`
	// ParentID is the country root the province region will be attached to.
	ParentID string `json:"parent_id"`
	// ProviderID is the tax provider's id and it must be REGISTERED; an id that
	// is not registered is rejected with a 422. Left empty, a province region
	// inherits the country's provider and a root region uses local
	// calculation.
	ProviderID string `json:"provider_id"`
	// Metadata is free-form metadata.
	Metadata map[string]any `json:"metadata"`
}

// createTaxRateRequest is the tax rate creation request.
type createTaxRateRequest struct {
	// TaxRegionID is the region the rate will be added to; it is required.
	//
	// Carrying it in the body is deliberate: a rate POSTed under
	// /admin/v1/tax-rates says in its own body which region it belongs to, and
	// were the same body moved to the region's sub-resource
	// (…/tax-regions/{id}/tax-rates) the path and the body could contradict
	// each other. In this module a rate is WRITTEN only over
	// /admin/v1/tax-rates; the endpoint under the region is READ-ONLY.
	TaxRegionID string `json:"tax_region_id"`
	// Name is the rate's display name; it is required.
	Name string `json:"name"`
	// Code is the reconciliation code; it may be left empty.
	Code string `json:"code"`
	// RateBps is the rate (basis points; 2000 = 20%).
	RateBps int32 `json:"rate_bps"`
	// IsDefault is whether this is the region's default rate.
	IsDefault bool `json:"is_default"`
	// Metadata is free-form metadata.
	Metadata map[string]any `json:"metadata"`
}

// updateTaxRateRequest is the tax rate update request.
//
// Every field is a pointer: a field that is not given DOES NOT CHANGE. Had a
// full body been required, a client that forgot to send rate_bps in its body
// would silently zero the rate.
type updateTaxRateRequest struct {
	// Name is the new name; when null/absent the name does not change.
	Name *string `json:"name"`
	// Code is the new reconciliation code; when null/absent it does not change.
	// To REMOVE the code an empty string is sent.
	Code *string `json:"code"`
	// RateBps is the new rate (basis points); when null/absent the rate does
	// not change.
	RateBps *int32 `json:"rate_bps"`
	// IsDefault is the default flag; when null/absent it does not change.
	IsDefault *bool `json:"is_default"`
	// Metadata is the new metadata; when null/absent the metadata does not
	// change.
	Metadata map[string]any `json:"metadata"`
}

// createTaxRateRuleRequest is the tax rule creation request.
type createTaxRateRuleRequest struct {
	// Reference is the kind of the item: "product", "product_type" or
	// "shipping_option".
	Reference string `json:"reference"`
	// ReferenceID is the id within that kind; it is required.
	ReferenceID string `json:"reference_id"`
}

// toTaxRegionDTO converts the region model into the response body.
func toTaxRegionDTO(region models.TaxRegion) taxRegionDTO {
	return taxRegionDTO{
		ID:           region.ID,
		CountryCode:  region.CountryCode,
		ProvinceCode: region.ProvinceCode,
		ParentID:     region.ParentID,
		ProviderID:   region.ProviderID,
		Metadata:     region.Metadata,
		CreatedAt:    region.CreatedAt,
		UpdatedAt:    region.UpdatedAt,
	}
}

// toTaxRateDTO converts the rate model into the response body.
func toTaxRateDTO(rate models.TaxRate) taxRateDTO {
	return taxRateDTO{
		ID:          rate.ID,
		TaxRegionID: rate.TaxRegionID,
		Name:        rate.Name,
		Code:        rate.Code,
		RateBps:     rate.RateBps,
		IsDefault:   rate.IsDefault,
		Metadata:    rate.Metadata,
		CreatedAt:   rate.CreatedAt,
		UpdatedAt:   rate.UpdatedAt,
	}
}

// toTaxRateRuleDTO converts the rule model into the response body.
func toTaxRateRuleDTO(rule models.TaxRateRule) taxRateRuleDTO {
	return taxRateRuleDTO{
		ID:          rule.ID,
		TaxRateID:   rule.TaxRateID,
		Reference:   rule.Reference.String(),
		ReferenceID: rule.ReferenceID,
		CreatedAt:   rule.CreatedAt,
	}
}

// toCreateTaxRegionInput converts the request body into the service input.
//
// NO validation is done: the service decides on validity, and having a single
// validation site makes HTTP and the cross-module call see the same rules.
func toCreateTaxRegionInput(req createTaxRegionRequest) service.CreateTaxRegionInput {
	return service.CreateTaxRegionInput{
		CountryCode:  req.CountryCode,
		ProvinceCode: req.ProvinceCode,
		ParentID:     req.ParentID,
		ProviderID:   req.ProviderID,
		Metadata:     req.Metadata,
	}
}

// toCreateTaxRateInput converts the request body into the service input.
func toCreateTaxRateInput(req createTaxRateRequest) service.CreateTaxRateInput {
	return service.CreateTaxRateInput{
		TaxRegionID: req.TaxRegionID,
		Name:        req.Name,
		Code:        req.Code,
		RateBps:     req.RateBps,
		IsDefault:   req.IsDefault,
		Metadata:    req.Metadata,
	}
}

// toUpdateTaxRateInput converts the request body into the service input.
func toUpdateTaxRateInput(req updateTaxRateRequest) service.UpdateTaxRateInput {
	return service.UpdateTaxRateInput{
		Name:      req.Name,
		Code:      req.Code,
		RateBps:   req.RateBps,
		IsDefault: req.IsDefault,
		Metadata:  req.Metadata,
	}
}
