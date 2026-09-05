package service

import (
	"context"
	"maps"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// The field names offered by the Query provider.
const (
	// FieldID is the record's identifier; Query joins on this field.
	FieldID = query.IDField
	// FieldName is the option's display name.
	FieldName = "name"
	// FieldProviderID is the identifier of the provider that will execute the
	// option.
	FieldProviderID = "provider_id"
	// FieldProfileID is the shipping profile the option is bound to.
	FieldProfileID = "shipping_profile_id"
	// FieldPriceType says where the fee comes from ("flat"/"calculated").
	FieldPriceType = "price_type"
	// FieldAmount is the fixed fee of "flat" options (minor unit).
	FieldAmount = "amount"
	// FieldCurrencyCode is the ISO 4217 currency code.
	FieldCurrencyCode = "currency_code"
	// FieldRegionID is the region the option is valid in; if empty, every
	// region.
	FieldRegionID = "region_id"
	// FieldIsReturn says the option is for a return shipment.
	FieldIsReturn = "is_return"
	// FieldAdminOnly says the option does not reach the storefront surface.
	FieldAdminOnly = "admin_only"
	// FieldCreatedAt is the creation time.
	FieldCreatedAt = "created_at"
	// FieldUpdatedAt is the last update time.
	FieldUpdatedAt = "updated_at"
)

// optionFieldGetters are the extractors of the offered fields.
//
// Having the field set defined in a single place makes it impossible for
// validation and production to diverge: if a field that is not here is
// requested, errors.Invalid is returned (ADR 0004), and every field that is
// here can also be produced.
//
// Data and Metadata are DELIBERATELY not offered. Data is the provider's
// internal configuration and must never appear on a cross-module read surface;
// Metadata is free-form data the caller put there, and carrying a field with no
// schema would make the records Query joins unpredictable.
//
// [FieldAdminOnly], on the other hand, IS OFFERED: this surface is a
// cross-module READ surface, not a storefront response. The consumer needs to
// know whether an option reaches the storefront; the hiding on the storefront
// surface is done in the api package.
var optionFieldGetters = map[string]func(option models.ShippingOption) any{
	FieldID:           func(option models.ShippingOption) any { return option.ID },
	FieldName:         func(option models.ShippingOption) any { return option.Name },
	FieldProviderID:   func(option models.ShippingOption) any { return option.ProviderID },
	FieldProfileID:    func(option models.ShippingOption) any { return option.ShippingProfileID },
	FieldPriceType:    func(option models.ShippingOption) any { return option.PriceType.String() },
	FieldAmount:       func(option models.ShippingOption) any { return option.Amount },
	FieldCurrencyCode: func(option models.ShippingOption) any { return option.CurrencyCode },
	FieldRegionID:     func(option models.ShippingOption) any { return option.RegionID },
	FieldIsReturn:     func(option models.ShippingOption) any { return option.IsReturn },
	FieldAdminOnly:    func(option models.ShippingOption) any { return option.AdminOnly },
	FieldCreatedAt:    func(option models.ShippingOption) any { return option.CreatedAt },
	FieldUpdatedAt:    func(option models.ShippingOption) any { return option.UpdatedAt },
}

// QueryProvider is the read surface the fulfillment module opens to the Query
// layer.
//
// It is registered with the container under the name "shipping_option.query";
// Query resolves it BY NAME (ADR 0004). An order listing sees which shipping
// option an order was sent with through this provider and a link.
type QueryProvider struct {
	svc *Service
}

// That QueryProvider satisfies the core contract is verified at compile time;
// a signature drift does not survive until runtime.
var _ query.Provider = (*QueryProvider)(nil)

// NewQueryProvider produces a provider running on the given service.
func NewQueryProvider(svc *Service) *QueryProvider {
	return &QueryProvider{svc: svc}
}

// Entity returns the entity name the provider offers.
func (p *QueryProvider) Entity() string { return EntityName }

// List returns the root records.
//
// Supported filters: "region_id", "shipping_profile_id", "provider_id" and
// "price_type" (all text). Any other filter or an unrecognized field is
// rejected with errors.Invalid (ADR 0004).
//
// The limit is CLAMPED to [MaxLimit]; see [providerLimit]. The clamping is
// silent and returns no error, but it does mean the page size cannot be
// exceeded: the caller must not assume it received all the records, and must
// read a response that returns [MaxLimit] records as "there may be more".
func (p *QueryProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := validateFields(opts.Fields); err != nil {
		return nil, err
	}

	in := ListOptionsAdminInput{
		Page: Page{Limit: providerLimit(opts.Limit), Offset: int64(opts.Offset)},
	}
	// The filters are walked in order: iterating over the map would leave it
	// random which error is returned when several filters are invalid at once.
	for _, name := range slices.Sorted(maps.Keys(opts.Filters)) {
		value := opts.Filters[name]
		text, ok := value.(string)
		if !ok {
			return nil, errors.Invalid(CodeInvalidInput,
				"filter %q has to be text, %T given", name, value)
		}
		switch name {
		case FieldRegionID:
			in.RegionID = &text
		case FieldProfileID:
			in.ProfileID = &text
		case FieldProviderID:
			in.ProviderID = &text
		case FieldPriceType:
			in.PriceType = &text
		default:
			return nil, errors.Invalid(CodeInvalidInput,
				"entity %q does not support the filter %q", EntityName, name)
		}
	}

	options, _, err := p.svc.ListShippingOptions(ctx, in)
	if err != nil {
		return nil, err
	}
	return records(options, opts.Fields), nil
}

// FetchByIDs returns the records of the given identifiers as a BATCH.
// No record is returned for an identifier that is not found; that is not an
// error.
func (p *QueryProvider) FetchByIDs(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	if err := validateFields(fields); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []query.Record{}, nil
	}

	options, err := p.svc.ListShippingOptionsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return records(options, fields), nil
}

// records turns the options into records with the requested fields.
// If fields is empty, ALL offered fields are returned.
func records(options []models.ShippingOption, fields []string) []query.Record {
	selected := fields
	if len(selected) == 0 {
		selected = slices.Sorted(maps.Keys(optionFieldGetters))
	}

	out := make([]query.Record, 0, len(options))
	// The slice is walked BY INDEX: walking by value would copy the whole option
	// struct on every iteration and the cost would grow with the record count.
	for i := range options {
		record := make(query.Record, len(selected))
		for _, name := range selected {
			record[name] = optionFieldGetters[name](options[i])
		}
		out = append(out, record)
	}
	return out
}

// providerLimit clamps the core's limit value to the provider's page ceiling.
//
// In the core contract ([query.ListOptions]) 0 means "UNLIMITED"; this provider
// does not offer unlimited listing, because an unlimited root query would pull
// the entire option table into memory. An unlimited request is therefore turned
// into [MaxLimit] — NOT into [DefaultLimit]: the caller explicitly said "I want
// them all" and should get the most it can. A nonsensical negative value is put
// in the same bucket: on this path the limit is not a client input but a number
// coming from another module's query definition, and rejecting it would bring
// down the whole read.
func providerLimit(limit int) int64 {
	if limit <= 0 || int64(limit) > MaxLimit {
		return MaxLimit
	}
	return int64(limit)
}

// validateFields verifies that all the requested fields are offered.
func validateFields(fields []string) error {
	for _, name := range fields {
		if _, ok := optionFieldGetters[name]; !ok {
			return errors.Invalid(CodeInvalidInput,
				"entity %q does not offer the field %q", EntityName, name)
		}
	}
	return nil
}
