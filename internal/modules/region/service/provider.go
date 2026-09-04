package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// Entity is the entity name region opens to the Query layer.
// The provider is registered in the container under the name "region" +
// query.ProviderSuffix.
const Entity = "region"

// The field names the provider offers.
const (
	fieldID             = "id"
	fieldName           = "name"
	fieldCurrencyCode   = "currency_code"
	fieldAutomaticTaxes = "automatic_taxes"
	fieldTaxRate        = "tax_rate"
	fieldCurrency       = "currency"
	fieldCountries      = "countries"
	fieldCreatedAt      = "created_at"
	fieldUpdatedAt      = "updated_at"
)

// The field names of the sub-records (currency, country).
const (
	fieldCode          = "code"
	fieldSymbol        = "symbol"
	fieldDecimalDigits = "decimal_digits"
)

// supportedFields are the fields the provider knows; if another field is asked
// for, errors.Invalid is returned (ADR 0004: field validation belongs to the
// provider).
var supportedFields = []string{
	fieldID, fieldName, fieldCurrencyCode, fieldAutomaticTaxes, fieldTaxRate,
	fieldCurrency, fieldCountries, fieldCreatedAt, fieldUpdatedAt,
}

// QueryProvider opens the regions to the Query layer (ADR 0004).
//
// The records come back TOGETHER WITH their currency and their countries. This
// is deliberate: the provider's consumers are the storefront's region/currency
// selection and (in Phase 5) the cart's region expansion; neither of them
// thinks of a region apart from its currency. Had they come back separately, a
// second round trip would be needed for every region, and preventing exactly
// that is what Query's N+1 ban is for.
//
// If they are not wanted they can be left out with Fields; in that case the
// related queries are NOT made AT ALL.
//
// # Why the decimal digits are here
//
// The "currency" sub-record in the record carries
// [models.Currency.DecimalDigits]. Cart and price amounts are minor unit
// integers; a storefront that reads a region and does not learn the division
// factor from the same response would be forced either to go to a second
// endpoint or to assume a fixed 100 — the latter would show yen amounts a
// hundred times too small.
//
// The interface is defined in internal/core/query; this type only satisfies the
// signature and tells the core nothing (the provider side of ADR 0001).
type QueryProvider struct {
	svc *Service
}

var _ query.Provider = (*QueryProvider)(nil)

// NewQueryProvider produces a provider that works over the given service.
func NewQueryProvider(svc *Service) *QueryProvider {
	return &QueryProvider{svc: svc}
}

// Entity returns the entity name the provider offers.
func (p *QueryProvider) Entity() string { return Entity }

// List returns the root region records.
//
// The only supported filter is "id"; its value can be a single string or a
// string slice. Any other filter returns errors.Invalid.
//
// If a limit of zero is given, the module's default page size is applied
// INSTEAD OF the "unlimited" of the Query contract and [MaxLimit] cannot be
// exceeded: an unlimited root list would take the whole table into memory in a
// single request.
func (p *QueryProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := p.svc.ready(); err != nil {
		return nil, err
	}
	fields, err := normalizeFields(opts.Fields)
	if err != nil {
		return nil, err
	}
	ids, err := idFilter(opts.Filters)
	if err != nil {
		return nil, err
	}

	if ids != nil {
		// If there is an id filter no paging is applied: the caller has already
		// named an exact set.
		return p.fetch(ctx, ids, fields)
	}

	limit, offset, err := normalizePaging(clampToInt32(opts.Limit), clampToInt32(opts.Offset))
	if err != nil {
		return nil, err
	}

	regions, _, err := p.svc.repo.ListRegions(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, regions, fields)
}

// FetchByIDs returns the records corresponding to the given ids in a SINGLE
// round trip.
//
// No record comes back for an id that is not found; this is not an error
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

// fetch reads the id set and turns it into records.
func (p *QueryProvider) fetch(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	if len(ids) == 0 {
		return []query.Record{}, nil
	}

	regions, err := p.svc.repo.GetRegionsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, regions, fields)
}

// records turns the regions into Query records; if they are needed it fetches
// the currencies and the countries in bulk with a SINGLE query.
//
// Bulk fetching produces a CONSTANT number of round trips per expansion no
// matter how many regions come back: one region and a hundred regions make the
// same number of queries. Querying per region would be putting the N+1 that
// Query structurally prevents back inside the provider.
func (p *QueryProvider) records(
	ctx context.Context,
	regions []models.Region,
	fields []string,
) ([]query.Record, error) {
	records := make([]query.Record, 0, len(regions))
	if len(regions) == 0 {
		return records, nil
	}

	currencies := map[string]models.Currency{}
	if slices.Contains(fields, fieldCurrency) {
		codes := make([]string, 0, len(regions))
		for _, region := range regions {
			if !slices.Contains(codes, region.CurrencyCode) {
				codes = append(codes, region.CurrencyCode)
			}
		}

		fetched, err := p.svc.repo.GetCurrenciesByCodes(ctx, codes)
		if err != nil {
			return nil, err
		}
		for _, currency := range fetched {
			currencies[currency.Code] = currency
		}
	}

	countries := map[string][]models.Country{}
	if slices.Contains(fields, fieldCountries) {
		ids := make([]string, 0, len(regions))
		for _, region := range regions {
			ids = append(ids, region.ID)
		}

		fetched, err := p.svc.repo.ListCountriesByRegions(ctx, ids)
		if err != nil {
			return nil, err
		}
		countries = fetched
	}

	for _, region := range regions {
		record := make(query.Record, len(fields))
		for _, field := range fields {
			switch field {
			case fieldID:
				record[fieldID] = region.ID
			case fieldName:
				record[fieldName] = region.Name
			case fieldCurrencyCode:
				record[fieldCurrencyCode] = region.CurrencyCode
			case fieldAutomaticTaxes:
				record[fieldAutomaticTaxes] = region.AutomaticTaxes
			case fieldTaxRate:
				record[fieldTaxRate] = region.TaxRate
			case fieldCreatedAt:
				record[fieldCreatedAt] = region.CreatedAt
			case fieldUpdatedAt:
				record[fieldUpdatedAt] = region.UpdatedAt
			case fieldCurrency:
				record[fieldCurrency] = currencyRecord(currencies, region.CurrencyCode)
			case fieldCountries:
				record[fieldCountries] = countryRecords(countries[region.ID])
			}
		}
		records = append(records, record)
	}
	return records, nil
}

// currencyRecord turns the currency into a sub-record; if it was not found it
// returns nil.
//
// Returning nil is deliberate: putting an empty record in place of a missing
// currency would show the decimal digits as 0 and would therefore show amounts
// at the wrong scale. Because of the foreign key this situation normally cannot
// arise.
func currencyRecord(currencies map[string]models.Currency, code string) map[string]any {
	currency, ok := currencies[code]
	if !ok {
		return nil
	}
	return map[string]any{
		fieldCode:          currency.Code,
		fieldSymbol:        currency.Symbol,
		fieldName:          currency.Name,
		fieldDecimalDigits: currency.DecimalDigits,
	}
}

// countryRecords turns the countries into sub-records.
//
// For a region that has no countries it returns an empty (non-nil) slice;
// seeing [] instead of null in JSON is a uniform surface for the consumer.
func countryRecords(countries []models.Country) []map[string]any {
	out := make([]map[string]any, 0, len(countries))
	for i := range countries {
		out = append(out, map[string]any{
			fieldCode: countries[i].Code,
			fieldName: countries[i].Name,
		})
	}
	return out
}

// normalizeFields validates the requested fields; an empty list means ALL the
// fields.
//
// The id field is ADDED to the list even if it was not asked for: Query joins
// the records over [query.IDField] and a record without an id would end in
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

// idFilter extracts the id set from the filters.
//
// If there is no filter it returns nil (no id filter is applied); if there is a
// filter other than "id" it returns errors.Invalid. An empty slice carries a
// meaning SEPARATE from nil: it means "no id at all" and an empty result comes
// back.
func idFilter(filters map[string]any) ([]string, error) {
	if len(filters) == 0 {
		return nil, nil
	}

	var ids []string
	for name, value := range filters {
		if name != fieldID {
			return nil, errors.Invalid(CodeInvalidInput,
				"filter %q is not supported by the %s provider (supported: %q)", name, Entity, fieldID)
		}
		switch typed := value.(type) {
		case string:
			ids = []string{typed}
		case []string:
			ids = slices.Clone(typed)
			if ids == nil {
				ids = []string{}
			}
		default:
			return nil, errors.Invalid(CodeInvalidInput,
				"filter %q has to be a string or a string slice, %T given", fieldID, value)
		}
	}
	return ids, nil
}
