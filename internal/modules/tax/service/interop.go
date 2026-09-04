package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// This file is the tax module's CROSS-MODULE surface (ADR 0001, ADR 0006).
//
// internal/workflows/cart needs tax while computing the cart total, but neither
// can that package import this module nor this module that package. The
// solution is the same as the interop.go in the region/cart/payment/order/
// inventory modules: publish a surface that uses ONLY PRIMITIVE and stdlib
// types. The consumer defines its own narrow interface, this type satisfies it
// STRUCTURALLY, and it is resolved from the container under the name
// "tax.interop".
//
// The reason is Go's structural conformance rule: because the consumer cannot
// import tax, it cannot name a type such as service.CalculateTaxInput in its
// signature; the moment it names one, that becomes a DIFFERENT type defined in
// its own package and the concrete service does not satisfy the consumer's
// interface.
//
// Composite data (the item list and the per-item tax) travels as JSON and the
// schema is declared EXPLICITLY BELOW. It MUST be exactly the same as the
// schema on the consumer side; because this module cannot import the workflow
// package the compiler cannot check the conformance, and the conformance can
// only be proven by an integration test.
//
// # Why the surface has two methods
//
// [Interop.CalculateTaxJSON] is the full calculation: province, rule, shipping
// and the per-item rate. [Interop.RateForCountry] is the PLAIN path and is the
// direct counterpart of the region module's TEMPORARY RegionTax method — it is
// for the caller that holds nothing but a country code and wants a single rate.
// Keeping the two apart is deliberate: it is needless for the plain path to pay
// the cost of JSON encoding/decoding, and simplifying the full calculation's
// signature "with optional fields" would be hiding two different contracts in
// one signature.

// Code constants; specific to the interop surface.
const (
	// CodeInteropRequestInvalid reports that the incoming JSON request could
	// not be decoded.
	CodeInteropRequestInvalid = "tax_interop_request_invalid"
	// CodeInteropResponseInvalid reports that the response could not be
	// encoded; it indicates an internal inconsistency and does not arise in the
	// normal flow.
	CodeInteropResponseInvalid = "tax_interop_response_invalid"
)

// interopRequest is the JSON schema of the [Interop.CalculateTaxJSON] request.
//
// Example:
//
//	{
//	  "country_code": "TR",
//	  "province_code": "34",
//	  "items": [
//	    {"id": "li_1", "product_id": "prod_1", "product_type_id": "ptyp_1", "amount": 3000}
//	  ],
//	  "shipping": {"option_id": "sopt_1", "amount": 2500, "taxable": false}
//	}
type interopRequest struct {
	// CountryCode is the ISO 3166-1 alpha-2 code; it is required.
	CountryCode string `json:"country_code"`
	// ProvinceCode is the province/state code; it is optional.
	ProvinceCode string `json:"province_code"`
	// Items are the items to be taxed.
	Items []interopItem `json:"items"`
	// Shipping is the shipping line.
	Shipping interopShipping `json:"shipping"`
}

// interopItem is the JSON schema of one taxable item.
type interopItem struct {
	// ID is the item's id ON THE CALLER's side (e.g. a cart line) and comes
	// back unchanged in the response.
	ID string `json:"id"`
	// ProductID is the product id for rule matching; it may be left empty.
	ProductID string `json:"product_id"`
	// ProductTypeID is the product type for rule matching; it may be left empty.
	ProductTypeID string `json:"product_type_id"`
	// Amount is the taxable base: a minor unit INTEGER and AFTER DISCOUNT. This
	// module does not see the discount.
	Amount int64 `json:"amount"`
}

// interopShipping is the JSON schema of the shipping line.
type interopShipping struct {
	// OptionID is the shipping option's id; it is there for rule matching.
	OptionID string `json:"option_id"`
	// Amount is the shipping amount (minor unit).
	Amount int64 `json:"amount"`
	// Taxable is whether shipping is taxed or not; it DEFAULTS to false and
	// when the field is not sent shipping does not enter the base.
	Taxable bool `json:"taxable"`
}

// interopResponse is the JSON schema of the [Interop.CalculateTaxJSON]
// response.
//
// Example:
//
//	{
//	  "region_id": "taxreg_01J…",
//	  "region_found": true,
//	  "provider_id": "local",
//	  "tax_total": 600,
//	  "items": [
//	    {"id": "li_1", "rate_id": "taxrate_01J…", "rate_bps": 2000,
//	     "taxable_amount": 3000, "tax_amount": 600}
//	  ],
//	  "shipping": {"id": "_shipping", "rate_id": "", "rate_bps": 0,
//	               "taxable_amount": 0, "tax_amount": 0}
//	}
//
// The identity always holds: tax_total = Σ(items[i].tax_amount) +
// shipping.tax_amount.
type interopResponse struct {
	// RegionID is the MOST SPECIFIC region the calculation rests on; empty when
	// there is no region.
	RegionID string `json:"region_id"`
	// RegionFound is whether a tax region belonging to the country was found.
	//
	// When false the tax is zero BECAUSE THERE IS NO CONFIGURATION; the field
	// is required so that this can be told apart from the rate genuinely being
	// zero.
	RegionFound bool `json:"region_found"`
	// ProviderID is the id of the provider that did the calculation; empty when
	// there is no region.
	ProviderID string `json:"provider_id"`
	// TaxTotal is the total tax (minor unit).
	TaxTotal int64 `json:"tax_total"`
	// Items is the per-item tax; it comes back IN THE REQUEST's ORDER.
	Items []interopItemTax `json:"items"`
	// Shipping is the shipping line's tax.
	Shipping interopItemTax `json:"shipping"`
}

// interopItemTax is the JSON schema of one line's calculated tax.
type interopItemTax struct {
	// ID is the line's id; on the shipping line it is [ShippingLineID].
	ID string `json:"id"`
	// RateID is the id of the applied rate; empty when no rate was found.
	RateID string `json:"rate_id"`
	// RateBps is the applied rate (BASIS POINTS; 2000 = 20%). Being basis
	// points is deliberate: whether the value "rate": 20 is 20% or 0.2 would
	// stay ambiguous and would produce a hundredfold error on the client side.
	RateBps int32 `json:"rate_bps"`
	// TaxableAmount is the base the tax was calculated on (minor unit).
	TaxableAmount int64 `json:"taxable_amount"`
	// TaxAmount is the calculated tax (minor unit).
	TaxAmount int64 `json:"tax_amount"`
}

// Interop turns the tax service into a PRIMITIVE cross-module surface.
//
// It makes no decisions: it only translates the signature and the JSON schema.
// All the business rules stay on [Service]; adding a rule here would mean the
// same rule diverging in two places.
type Interop struct {
	svc *Service
}

// NewInterop sets up the cross-module surface for the given service.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// CalculateTaxJSON decodes the given request, calculates the tax and returns
// the result as JSON.
//
// The request schema is on the [interopRequest] type and the response schema on
// [interopResponse], each defined IN ONE PLACE; both godocs carry an example
// body.
//
// # Unknown fields are rejected
//
// A field ignored in silence means a base the caller thought it had sent never
// entering the calculation. Because the two sides cannot import each other the
// compiler cannot see this mismatch; strict decoding makes the mismatch surface
// as an explicit error on the first call.
//
// # Numbers
//
// Amounts are INTEGERS and are decoded as such; the schema's fields are int64.
// A floating point base (e.g. 30.5) gives a decoding error — it is not silently
// rounded (plan Section 8).
func (i *Interop) CalculateTaxJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error) {
	if i == nil || i.svc == nil {
		return nil, errors.Unavailable(CodeUnconfigured, "the tax service is not configured")
	}

	req, err := decodeInteropRequest(request)
	if err != nil {
		return nil, err
	}

	items := make([]TaxableItem, 0, len(req.Items))
	for idx := range req.Items {
		items = append(items, TaxableItem{
			ID:            req.Items[idx].ID,
			ProductID:     req.Items[idx].ProductID,
			ProductTypeID: req.Items[idx].ProductTypeID,
			Amount:        req.Items[idx].Amount,
		})
	}

	result, err := i.svc.CalculateTax(ctx, CalculateTaxInput{
		CountryCode:  req.CountryCode,
		ProvinceCode: req.ProvinceCode,
		Items:        items,
		Shipping: ShippingInput{
			OptionID: req.Shipping.OptionID,
			Amount:   req.Shipping.Amount,
			Taxable:  req.Shipping.Taxable,
		},
	})
	if err != nil {
		return nil, err
	}

	resp := interopResponse{
		RegionID:    result.RegionID,
		RegionFound: result.RegionFound,
		ProviderID:  result.ProviderID,
		TaxTotal:    result.TaxTotal,
		Items:       make([]interopItemTax, 0, len(result.Items)),
		Shipping:    toInteropItemTax(result.Shipping),
	}
	for idx := range result.Items {
		resp.Items = append(resp.Items, toInteropItemTax(result.Items[idx]))
	}

	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInteropResponseInvalid,
			"the tax result could not be converted to JSON")
	}
	return payload, nil
}

// RateForCountry returns a country's DEFAULT tax rate in basis points.
//
// It is the direct counterpart of the region module's TEMPORARY RegionTax
// method and exists for the plainest path of the cart flow: a caller holding
// nothing but a country code wants a single rate.
//
// The rate returned is the DEFAULT rate of the country ROOT. Province regions,
// rules and shipping are NOT EVALUATED on this path; a caller that needs them
// must use [Interop.CalculateTaxJSON]. The calculation is made from the local
// table and the region's provider is NOT CALLED — going out to an external tax
// service merely to ask for one rate would mean a network call on every round
// of the cart.
//
// found is the second return value and separates two cases: the country has no
// tax region (or has no default rate), versus the rate genuinely being zero.
// While found is false the rate is always zero.
//
// Its counterpart on the consumer side:
//
//	type TaxRateReader interface {
//	    RateForCountry(ctx context.Context, countryCode string) (int32, bool, error)
//	}
func (i *Interop) RateForCountry(ctx context.Context, countryCode string) (rateBps int32, found bool, err error) {
	if i == nil || i.svc == nil {
		return 0, false, errors.Unavailable(CodeUnconfigured, "the tax service is not configured")
	}
	return i.svc.DefaultRateForCountry(ctx, countryCode)
}

// decodeInteropRequest decodes the request body into the schema.
func decodeInteropRequest(request json.RawMessage) (interopRequest, error) {
	if len(request) == 0 {
		return interopRequest{}, errors.Invalid(CodeInteropRequestInvalid,
			"the tax calculation request cannot be empty")
	}

	var req interopRequest
	dec := json.NewDecoder(bytes.NewReader(request))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return interopRequest{}, errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"the tax calculation request could not be decoded")
	}

	// A single JSON document is expected; were a second document following it
	// ignored in silence, the caller would think what it sent had been
	// processed.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return interopRequest{}, errors.Invalid(CodeInteropRequestInvalid,
			"the tax calculation request must be a single JSON document")
	}
	return req, nil
}

// toInteropItemTax converts a result line into the JSON schema.
//
// The conversion is a direct type conversion because the fields of the two
// types are EXACTLY the same (Go permits conversion between structs that differ
// only in their tags). This is a deliberate choice: the moment [ItemTax] gains
// or loses a field the conversion DOES NOT COMPILE and a decision about what
// the JSON schema is to be has to be made explicitly. A mapping written field
// by field would instead leave the new field out in silence.
func toInteropItemTax(item ItemTax) interopItemTax {
	return interopItemTax(item)
}
