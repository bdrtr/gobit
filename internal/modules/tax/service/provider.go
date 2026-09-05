package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// Entity is the entity name tax opens to the Query layer.
// The provider is registered in the container under the name "tax_region" +
// query.ProviderSuffix.
const Entity = "tax_region"

// The field names the provider offers.
const (
	fieldID           = "id"
	fieldCountryCode  = "country_code"
	fieldProvinceCode = "province_code"
	fieldParentID     = "parent_id"
	fieldProviderID   = "provider_id"
	fieldMetadata     = "metadata"
	fieldRates        = "rates"
	fieldCreatedAt    = "created_at"
	fieldUpdatedAt    = "updated_at"
)

// The field names of the sub-records (the rates).
const (
	fieldName      = "name"
	fieldCode      = "code"
	fieldRateBps   = "rate_bps"
	fieldIsDefault = "is_default"
)

// supportedFields are the fields the provider recognizes; when any other field
// is requested errors.Invalid is returned (ADR 0004: field validation belongs
// to the provider).
var supportedFields = []string{
	fieldID, fieldCountryCode, fieldProvinceCode, fieldParentID, fieldProviderID,
	fieldMetadata, fieldRates, fieldCreatedAt, fieldUpdatedAt,
}

// QueryProvider opens tax regions to the Query layer (ADR 0004).
//
// When asked for, the records come back WITH THEIR RATES. Had they come back
// separately, a second round trip per region would be needed, and Query's
// no-N+1 rule exists precisely to prevent that. When they are not wanted they
// are left out via Fields; in that case the rate query is NOT MADE at all.
//
// # Why a tax region must be queryable
//
// An admin screen asks "in which countries is tax configured" and a report asks
// "what was this order's region" of this layer. Tax CALCULATION, however, does
// not go through here: the calculation is [Service.CalculateTax] and Query's
// loosely typed record surface is not suitable for money arithmetic.
//
// The interface is defined in core/query; this type only satisfies the
// signature and tells the core nothing (the provider side of ADR 0001).
type QueryProvider struct {
	svc *Service
}

var _ query.Provider = (*QueryProvider)(nil)

// NewQueryProvider produces a provider that works on the given service.
func NewQueryProvider(svc *Service) *QueryProvider {
	return &QueryProvider{svc: svc}
}

// Entity returns the entity name the provider offers.
func (p *QueryProvider) Entity() string { return Entity }

// List returns the root tax region records.
//
// The supported filters are "id" and "country_code"; the "id" value may be a
// single string or a string slice. Any other filter returns errors.Invalid. The
// "id" filter is used ON ITS OWN: a narrowing placed beside it is not dropped
// in silence, the combination is rejected (see splitFilters).
//
// When a limit of zero is given, the module's default page size applies INSTEAD
// OF the "unlimited" of the Query contract, and [MaxLimit] cannot be exceeded:
// an unlimited root listing would take the whole table into memory in a single
// request.
func (p *QueryProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := p.svc.ready(); err != nil {
		return nil, err
	}
	fields, err := normalizeFields(opts.Fields)
	if err != nil {
		return nil, err
	}
	ids, country, err := splitFilters(opts.Filters)
	if err != nil {
		return nil, err
	}

	if ids != nil {
		// When there is an id filter no paging is applied: the caller has
		// already named an exact set.
		return p.fetch(ctx, ids, fields)
	}

	page, err := p.svc.ListTaxRegions(ctx, country, clampToInt32(opts.Limit), clampToInt32(opts.Offset))
	if err != nil {
		return nil, err
	}
	return p.records(ctx, page.Items, fields)
}

// FetchByIDs returns the records corresponding to the given ids in a SINGLE
// round trip.
//
// No record is returned for an id that is not found; this is not an error
// (ADR 0004).
func (p *QueryProvider) FetchByIDs(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	if err := p.svc.ready(); err != nil {
		return nil, err
	}
	normalized, err := normalizeFields(fields)
	if err != nil {
		return nil, err
	}
	return p.fetch(ctx, ids, normalized)
}

// fetch reads the id set and converts it into records.
func (p *QueryProvider) fetch(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	if len(ids) == 0 {
		return []query.Record{}, nil
	}

	regions, err := p.svc.repo.GetTaxRegionsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, regions, fields)
}

// records converts the regions into Query records; when the rates are needed it
// fetches them in bulk with a SINGLE query.
//
// Bulk fetching produces a CONSTANT number of round trips per expansion, no
// matter how many regions come back. Querying per region would be putting the
// N+1 that Query structurally prevents back inside the provider.
func (p *QueryProvider) records(
	ctx context.Context,
	regions []models.TaxRegion,
	fields []string,
) ([]query.Record, error) {
	records := make([]query.Record, 0, len(regions))
	if len(regions) == 0 {
		return records, nil
	}

	rates := map[string][]models.TaxRate{}
	if slices.Contains(fields, fieldRates) {
		ids := make([]string, 0, len(regions))
		for i := range regions {
			ids = append(ids, regions[i].ID)
		}

		fetched, err := p.svc.repo.ListTaxRatesByRegions(ctx, ids)
		if err != nil {
			return nil, err
		}
		for i := range fetched {
			rates[fetched[i].TaxRegionID] = append(rates[fetched[i].TaxRegionID], fetched[i])
		}
	}

	for i := range regions {
		region := regions[i]
		record := make(query.Record, len(fields))
		for _, field := range fields {
			switch field {
			case fieldID:
				record[fieldID] = region.ID
			case fieldCountryCode:
				record[fieldCountryCode] = region.CountryCode
			case fieldProvinceCode:
				record[fieldProvinceCode] = region.Province()
			case fieldParentID:
				record[fieldParentID] = region.Parent()
			case fieldProviderID:
				record[fieldProviderID] = region.ProviderID
			case fieldMetadata:
				record[fieldMetadata] = region.Metadata
			case fieldRates:
				record[fieldRates] = rateRecords(rates[region.ID])
			case fieldCreatedAt:
				record[fieldCreatedAt] = region.CreatedAt
			case fieldUpdatedAt:
				record[fieldUpdatedAt] = region.UpdatedAt
			}
		}
		records = append(records, record)
	}
	return records, nil
}

// rateRecords converts the rates into sub-records.
//
// For a region that has no rates an empty (non-nil) slice is returned; seeing []
// instead of null in JSON is a uniform surface for the consumer.
//
// The rate is written as BASIS POINTS, with its unit in the field name: whether
// the value "rate": 20 is 20% or 0.2 would stay ambiguous.
func rateRecords(rates []models.TaxRate) []map[string]any {
	out := make([]map[string]any, 0, len(rates))
	for i := range rates {
		out = append(out, map[string]any{
			fieldID:        rates[i].ID,
			fieldName:      rates[i].Name,
			fieldCode:      rates[i].RateCode(),
			fieldRateBps:   rates[i].RateBps,
			fieldIsDefault: rates[i].IsDefault,
		})
	}
	return out
}

// normalizeFields validates the requested fields; an empty list means ALL
// fields.
//
// The id field is ADDED to the list even when it was not requested: Query joins
// records over [query.IDField] and a record without an id would end in
// errors.KindInternal.
func normalizeFields(fields []string) ([]string, error) {
	if len(fields) == 0 {
		return slices.Clone(supportedFields), nil
	}

	out := make([]string, 0, len(fields)+1)
	for _, field := range fields {
		if !slices.Contains(supportedFields, field) {
			return nil, errors.Invalid(CodeInvalidInput,
				"field %q does not exist in the %s provider (supported: %v)", field, Entity, supportedFields)
		}
		if !slices.Contains(out, field) {
			out = append(out, field)
		}
	}
	if !slices.Contains(out, fieldID) {
		out = append(out, fieldID)
	}
	return out, nil
}

// splitFilters extracts the id set and the country filter out of the filters.
//
// When there is no id filter it returns nil (no id filtering is applied). An
// empty slice carries a meaning SEPARATE from nil: it means "no ids at all" and
// an empty result comes back.
//
// When the id filter is given TOGETHER WITH another filter, errors.Invalid is
// returned. The id path names an exact set and also skips paging; dropping the
// narrowing placed beside it IN SILENCE instead of applying it would mean the
// caller taking hold of a wider set than it asked for. Rejecting is the
// established convention in the repository (customer/service/provider.go, the
// same combination) and it is consistent with this provider's own principle
// too: while an unsupported filter is already being rejected, ignoring a
// SUPPORTED filter would be a contradiction.
func splitFilters(filters map[string]any) (ids []string, countryCode string, err error) {
	if len(filters) == 0 {
		return nil, "", nil
	}

	for name, value := range filters {
		switch name {
		case fieldID:
			ids, err = stringOrSlice(fieldID, value)
			if err != nil {
				return nil, "", err
			}
		case fieldCountryCode:
			code, ok := value.(string)
			if !ok {
				return nil, "", errors.Invalid(CodeInvalidInput,
					"filter %q has to be a string, %T given", fieldCountryCode, value)
			}
			countryCode = code
		default:
			return nil, "", errors.Invalid(CodeInvalidInput,
				"the %q filter is not supported by the %s provider (supported: %q, %q)",
				name, Entity, fieldID, fieldCountryCode)
		}
	}

	if ids != nil && len(filters) > 1 {
		return nil, "", errors.Invalid(CodeInvalidInput,
			"filter %q cannot be used together with other filters", fieldID)
	}
	return ids, countryCode, nil
}

// stringOrSlice converts a single string or a string slice into an id set.
func stringOrSlice(field string, value any) ([]string, error) {
	switch typed := value.(type) {
	case string:
		return []string{typed}, nil
	case []string:
		out := slices.Clone(typed)
		if out == nil {
			out = []string{}
		}
		return out, nil
	default:
		return nil, errors.Invalid(CodeInvalidInput,
			"filter %q has to be a string or a string slice, %T given", field, value)
	}
}
