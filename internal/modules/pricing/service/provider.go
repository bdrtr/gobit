package service

import (
	"context"
	"slices"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// Entity is the entity name pricing exposes to the Query layer.
// The provider is registered in the container under the name "price_set" +
// query.ProviderSuffix.
const Entity = "price_set"

// The field names the provider offers.
const (
	fieldID        = "id"
	fieldCreatedAt = "created_at"
	fieldUpdatedAt = "updated_at"
	fieldPrices    = "prices"
)

// The field names of the price sub-records.
const (
	fieldCurrencyCode = "currency_code"
	fieldAmount       = "amount"
	fieldMinQuantity  = "min_quantity"
	fieldMaxQuantity  = "max_quantity"
	fieldPriceListID  = "price_list_id"
)

// supportedFields are the fields the provider recognizes; if another field is
// requested errors.Invalid is returned (ADR 0004: field validation belongs to
// the provider).
var supportedFields = []string{fieldID, fieldCreatedAt, fieldUpdatedAt, fieldPrices}

// QueryProvider exposes price sets to the Query layer (ADR 0004).
//
// Records are returned TOGETHER WITH their prices. This is deliberate: the
// provider's only consumer is product's store listing, and a price set record
// without prices is of no use there — a second round would be needed, and
// Query's no-N+1 rule exists precisely to prevent that. If the prices are not
// wanted they can be left out with Fields.
//
// # Which prices are returned
//
// ONLY the prices valid "right now, for everyone" (see [listablePrices]). The
// provider is a read surface and carries no calculation context (currency,
// quantity, rule context); if it returned a price conditional on a context it
// does not carry, the consumer could not eliminate them and the storefront would
// show a price that pricing itself considers INVALID. A caller wanting a
// context-dependent price uses [Service.CalculateAmount]; the selection rule
// lives there, in a single place.
//
// The interface is defined in internal/core/query; this type merely satisfies
// the signature and declares nothing to the core (the provider side of ADR
// 0001).
type QueryProvider struct {
	svc *Service
}

var _ query.Provider = (*QueryProvider)(nil)

// NewQueryProvider produces a provider working on the given service.
func NewQueryProvider(svc *Service) *QueryProvider {
	return &QueryProvider{svc: svc}
}

// Entity returns the entity name the provider offers.
func (p *QueryProvider) Entity() string { return Entity }

// List returns the root price set records.
//
// The only supported filter is "id"; its value may be a single string or a
// string slice. Any other filter returns errors.Invalid.
//
// If limit is given as zero, the module's default page size is applied INSTEAD
// OF the "unlimited" of the Query contract, and [MaxLimit] cannot be exceeded: an
// unlimited root listing would pull the whole table into memory in a single
// request.
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
		// If there is an id filter, paging is not applied: the caller has
		// already named an exact set.
		return p.fetch(ctx, ids, fields)
	}

	limit, offset, err := normalizePaging(clampToInt32(opts.Limit), clampToInt32(opts.Offset))
	if err != nil {
		return nil, err
	}

	sets, _, err := p.svc.repo.ListPriceSets(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, sets, fields)
}

// FetchByIDs returns the records corresponding to the given ids in a SINGLE
// round.
//
// No record is returned for an id that is not found; that is not an error
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

	sets, err := p.svc.repo.GetPriceSetsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, sets, fields)
}

// records converts price sets into Query records; when needed it fetches the
// prices in bulk with a SINGLE query.
func (p *QueryProvider) records(
	ctx context.Context,
	sets []models.PriceSet,
	fields []string,
) ([]query.Record, error) {
	records := make([]query.Record, 0, len(sets))
	if len(sets) == 0 {
		return records, nil
	}

	pricesBySet := map[string][]models.Price{}
	if slices.Contains(fields, fieldPrices) {
		setIDs := make([]string, 0, len(sets))
		for _, set := range sets {
			setIDs = append(setIDs, set.ID)
		}

		candidatesBySet, err := p.svc.repo.ListPriceCandidatesBySets(ctx, setIDs)
		if err != nil {
			return nil, err
		}

		at := p.svc.clock()
		for setID, candidates := range candidatesBySet {
			pricesBySet[setID] = listablePrices(candidates, at)
		}
	}

	for _, set := range sets {
		record := make(query.Record, len(fields))
		for _, field := range fields {
			switch field {
			case fieldID:
				record[fieldID] = set.ID
			case fieldCreatedAt:
				record[fieldCreatedAt] = set.CreatedAt
			case fieldUpdatedAt:
				record[fieldUpdatedAt] = set.UpdatedAt
			case fieldPrices:
				record[fieldPrices] = priceRecords(pricesBySet[set.ID])
			}
		}
		records = append(records, record)
	}
	return records, nil
}

// listablePrices filters out of the candidates only those that are
// UNCONDITIONAL and valid at that moment.
//
// Both conditions are the very same as the elimination rule in the calculation
// (see eligible):
//
//   - If a price is bound to a list, the list must be usable. If the price of a
//     draft, ended, out-of-window or DELETED list were eliminated in the
//     calculation but visible in the read, the storefront would be showing an
//     unpublished campaign.
//   - The price MUST HAVE NO RULE. A rule looks at a context (region, customer
//     group …); the provider does not carry that context and cannot evaluate the
//     rule here. Ignoring a condition that cannot be evaluated would open the
//     segment price to everyone — the very same rationale as in matchRule.
func listablePrices(candidates []models.PriceCandidate, at time.Time) []models.Price {
	prices := make([]models.Price, 0, len(candidates))
	for i := range candidates {
		candidate := candidates[i]
		if len(candidate.Price.Rules) > 0 || !listAvailable(candidate, at) {
			continue
		}
		prices = append(prices, candidate.Price)
	}
	return prices
}

// priceRecords converts prices into sub-records.
//
// For a container with no id an empty (non-nil) slice is returned; seeing []
// instead of null in JSON is a uniform surface for the consumer.
func priceRecords(prices []models.Price) []map[string]any {
	out := make([]map[string]any, 0, len(prices))
	for i := range prices {
		price := &prices[i]
		record := map[string]any{
			fieldID:           price.ID,
			fieldCurrencyCode: price.CurrencyCode,
			fieldAmount:       price.Amount,
			fieldMinQuantity:  price.MinQuantity,
			fieldMaxQuantity:  price.MaxQuantity,
			fieldPriceListID:  price.PriceListID,
		}
		out = append(out, record)
	}
	return out
}

// normalizeFields validates the requested fields; an empty list means ALL
// fields.
//
// The id field is ADDED to the list even when it was not requested: Query joins
// records through [query.IDField] and a record without an id would end in
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

// idFilter extracts the id set out of the filters.
//
// If there is no filter it returns nil (no id filter is applied); if there is a
// filter other than "id" it returns errors.Invalid. An empty slice carries a
// meaning DISTINCT from nil: it means "no ids at all" and an empty result is
// returned.
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
