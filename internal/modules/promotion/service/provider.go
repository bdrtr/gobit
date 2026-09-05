package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// Entity is the entity name promotion exposes to the Query layer.
// The provider is registered in the container under the name "promotion" +
// query.ProviderSuffix.
const Entity = "promotion"

// The field names the provider offers.
const (
	fieldID          = "id"
	fieldCode        = "code"
	fieldIsAutomatic = "is_automatic"
	fieldType        = "type"
	fieldStatus      = "status"
	fieldCampaignID  = "campaign_id"
	fieldCreatedAt   = "created_at"
	fieldUpdatedAt   = "updated_at"
)

// supportedFields are the fields the provider recognizes; if any other field is
// requested errors.Invalid is returned (ADR 0004: field validation belongs to the
// provider).
//
// # What is NOT on the list, and why
//
//   - Rules (promotion_rule): the right-hand side of a rule is business information
//     (e.g. the identifier of a customer group) and the Query layer has no way of
//     telling whether a storefront or an admin surface is reading. An admin surface
//     that wants to see rules uses /admin/v1/promotions/{id}/rules.
//   - The application method: the amount/rate of the discount is in the same class
//     and is handed to a customer only through the coupon validation endpoint, with
//     the coupon code KNOWN.
//   - The usage counter and the campaign budget: how many times a coupon has been
//     used is a competitively sensitive number and must not leak from the read
//     surface.
//   - Metadata: it is free text and there is no knowing what has been put inside it.
var supportedFields = []string{
	fieldID, fieldCode, fieldIsAutomatic, fieldType,
	fieldStatus, fieldCampaignID, fieldCreatedAt, fieldUpdatedAt,
}

// QueryProvider exposes promotions to the Query layer (ADR 0004).
//
// # Only ACTIVE promotions are returned
//
// Both [QueryProvider.List] and [QueryProvider.FetchByIDs] apply the same filter:
// draft and inactive promotions are never returned. The rule has to be a single one,
// because the Query layer has no way of telling whether a storefront or an admin
// surface is reading, and being able to list the CODE of a draft coupon would give
// away an unpublished campaign.
//
// The same filter holding for FetchByIDs as well is a deliberate cost: if an order is
// bound to a promotion that was deactivated afterwards, NO record COMES BACK from
// this surface. The right source is the snapshot on the order side anyway — which
// discount an order received depends not on the promotion's state TODAY, but on that
// day's computation.
//
// The interface is defined in core/query; this type only satisfies the
// signature and tells the core nothing (the provider side of ADR 0001).
type QueryProvider struct {
	svc *Service
}

var _ query.Provider = (*QueryProvider)(nil)

// NewQueryProvider produces a provider running on the given service.
func NewQueryProvider(svc *Service) *QueryProvider {
	return &QueryProvider{svc: svc}
}

// Entity returns the entity name the provider offers.
func (p *QueryProvider) Entity() string { return Entity }

// List returns the root promotion records.
//
// The only supported filter is "id"; its value can be a single string or a string
// slice. Any other filter returns errors.Invalid.
//
// If a limit of zero is given, the module's default page size is applied INSTEAD of
// the "unbounded" of the Query contract, and [MaxLimit] cannot be exceeded: an
// unbounded root listing would pull the whole table into memory in a single request.
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
		// If there is an identifier filter no paging is applied: the caller has
		// already named an exact set.
		return p.fetch(ctx, ids, fields)
	}

	limit, offset, err := normalizePaging(clampToInt32(opts.Limit), clampToInt32(opts.Offset))
	if err != nil {
		return nil, err
	}

	active := string(models.PromotionActive)
	promotions, _, err := p.svc.repo.ListPromotions(ctx, &active, nil, limit, offset)
	if err != nil {
		return nil, err
	}
	return records(promotions, fields), nil
}

// FetchByIDs returns the records matching the given identifiers in a SINGLE round.
//
// No record is returned for an identifier that is not found (or is not active); this
// is not an error (ADR 0004).
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

// fetch reads the identifier set and turns it into records; it eliminates the ones
// that are not active.
func (p *QueryProvider) fetch(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	if len(ids) == 0 {
		return []query.Record{}, nil
	}

	promotions, err := p.svc.repo.GetPromotionsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	active := make([]models.Promotion, 0, len(promotions))
	for i := range promotions {
		if promotions[i].Status == models.PromotionActive {
			active = append(active, promotions[i])
		}
	}
	return records(active, fields), nil
}

// records turns promotions into Query records.
func records(promotions []models.Promotion, fields []string) []query.Record {
	out := make([]query.Record, 0, len(promotions))
	for i := range promotions {
		promo := &promotions[i]
		record := make(query.Record, len(fields))
		for _, field := range fields {
			switch field {
			case fieldID:
				record[fieldID] = promo.ID
			case fieldCode:
				record[fieldCode] = promo.Code
			case fieldIsAutomatic:
				record[fieldIsAutomatic] = promo.IsAutomatic
			case fieldType:
				record[fieldType] = string(promo.Type)
			case fieldStatus:
				record[fieldStatus] = string(promo.Status)
			case fieldCampaignID:
				record[fieldCampaignID] = promo.CampaignID
			case fieldCreatedAt:
				record[fieldCreatedAt] = promo.CreatedAt
			case fieldUpdatedAt:
				record[fieldUpdatedAt] = promo.UpdatedAt
			}
		}
		out = append(out, record)
	}
	return out
}

// normalizeFields validates the requested fields; an empty list means ALL fields.
//
// The identifier field is ADDED to the list even when it was not requested: Query
// joins records through [query.IDField] and a record without an identifier would end
// in errors.KindInternal.
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

// idFilter extracts the identifier set out of the filters.
//
// If there is no filter it returns nil (no identifier filter is applied); if there is
// a filter other than "id" it returns errors.Invalid. An empty slice carries a
// meaning DISTINCT from nil: it means "no identifiers" and returns an empty result.
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
