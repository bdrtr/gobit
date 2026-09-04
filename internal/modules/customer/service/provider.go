package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// Entity is the entity name customer opens to the Query layer.
// The provider is registered in the container under the name "customer" +
// query.ProviderSuffix.
const Entity = "customer"

// The field names the provider offers.
const (
	fieldID         = "id"
	fieldEmail      = "email"
	fieldFirstName  = "first_name"
	fieldLastName   = "last_name"
	fieldPhone      = "phone"
	fieldHasAccount = "has_account"
	fieldGroupIDs   = "group_ids"
	fieldMetadata   = "metadata"
	fieldCreatedAt  = "created_at"
	fieldUpdatedAt  = "updated_at"
)

// The filter names the provider recognizes.
const (
	filterID         = "id"
	filterEmail      = "email"
	filterHasAccount = "has_account"
	filterGroupID    = "group_id"
)

// supportedFields are the fields the provider recognizes; if another field is
// requested errors.Invalid is returned (ADR 0004: field validation belongs to
// the provider).
var supportedFields = []string{
	fieldID, fieldEmail, fieldFirstName, fieldLastName, fieldPhone,
	fieldHasAccount, fieldGroupIDs, fieldMetadata, fieldCreatedAt, fieldUpdatedAt,
}

// supportedFilters are the filters the provider recognizes.
var supportedFilters = []string{filterID, filterEmail, filterHasAccount, filterGroupID}

// QueryProvider opens the customers to the Query layer (ADR 0004).
//
// # Why together with the group ids
//
// Records are returned with the customer's GROUP IDS. The consumer of this is
// the price computation: pricing's rule context looks at the
// "customer_group_id" attribute, and for a cart to be priced it has to be known
// which segments the customer is in. Had the group ids been requested in a
// separate round, that would have meant a second call for every customer;
// Query's ban on N+1 exists precisely to prevent this. The ids are fetched in a
// single query for the whole SET, not per customer.
//
// If the group ids are not wanted they can be left out with Fields; in that
// case the membership query is NOT run at all.
//
// The interface is defined in internal/core/query; this type only satisfies the
// signature and tells the core nothing (the provider side of ADR 0001).
type QueryProvider struct {
	svc *Service
}

var _ query.Provider = (*QueryProvider)(nil)

// NewQueryProvider produces a provider working over the given service.
func NewQueryProvider(svc *Service) *QueryProvider {
	return &QueryProvider{svc: svc}
}

// Entity returns the entity name the provider offers.
func (p *QueryProvider) Entity() string { return Entity }

// List returns the root customer records.
//
// Supported filters: "id" (a string or a string slice), "email",
// "has_account", "group_id". The "id" filter CANNOT BE COMBINED WITH THE
// OTHERS — with an exact id set already named, a second filter would have meant
// the record the caller asked for is silently eliminated and the result comes
// back empty.
//
// If a zero limit is given, the module's default page size is applied INSTEAD
// of the "unlimited" in the Query contract: an unlimited root listing would
// pull the whole customer table into memory in a single request.
func (p *QueryProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := p.svc.ready(); err != nil {
		return nil, err
	}
	fields, err := normalizeFields(opts.Fields)
	if err != nil {
		return nil, err
	}
	ids, filter, err := splitFilters(opts.Filters)
	if err != nil {
		return nil, err
	}

	if ids != nil {
		// If there is an id filter, no pagination is applied: the caller has
		// already named an exact set.
		return p.fetch(ctx, ids, fields)
	}

	limit, offset, err := normalizePaging(clampToInt64(opts.Limit), clampToInt64(opts.Offset))
	if err != nil {
		return nil, err
	}

	customers, _, err := p.svc.repo.ListCustomers(ctx, filter, limit, offset)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, customers, fields)
}

// FetchByIDs returns the records corresponding to the given ids in a SINGLE
// round.
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

	customers, err := p.svc.repo.GetCustomersByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, customers, fields)
}

// records converts the customers into Query records; if needed it fetches the
// group ids in bulk with a SINGLE query.
func (p *QueryProvider) records(
	ctx context.Context,
	customers []models.Customer,
	fields []string,
) ([]query.Record, error) {
	records := make([]query.Record, 0, len(customers))
	if len(customers) == 0 {
		return records, nil
	}

	groupsByCustomer := map[string][]string{}
	if slices.Contains(fields, fieldGroupIDs) {
		ids := make([]string, 0, len(customers))
		for i := range customers {
			ids = append(ids, customers[i].ID)
		}

		var err error
		groupsByCustomer, err = p.svc.repo.GroupIDsOfCustomers(ctx, ids)
		if err != nil {
			return nil, err
		}
	}

	for i := range customers {
		c := &customers[i]
		record := make(query.Record, len(fields))
		for _, field := range fields {
			switch field {
			case fieldID:
				record[fieldID] = c.ID
			case fieldEmail:
				record[fieldEmail] = c.Email
			case fieldFirstName:
				record[fieldFirstName] = c.FirstName
			case fieldLastName:
				record[fieldLastName] = c.LastName
			case fieldPhone:
				record[fieldPhone] = c.Phone
			case fieldHasAccount:
				record[fieldHasAccount] = c.HasAccount
			case fieldGroupIDs:
				record[fieldGroupIDs] = groupIDs(groupsByCustomer[c.ID])
			case fieldMetadata:
				record[fieldMetadata] = c.Metadata
			case fieldCreatedAt:
				record[fieldCreatedAt] = c.CreatedAt
			case fieldUpdatedAt:
				record[fieldUpdatedAt] = c.UpdatedAt
			}
		}
		records = append(records, record)
	}
	return records, nil
}

// groupIDs prepares the group ids for the record.
//
// For a customer with no groups an empty (non-nil) slice is returned; seeing []
// instead of null in JSON is a uniform surface for the consumer. The slice is
// also COPIED: Query records are shallow copies, and if the slice itself were
// shared the callers would see the same backing array.
func groupIDs(ids []string) []string {
	if len(ids) == 0 {
		return []string{}
	}
	return slices.Clone(ids)
}

// normalizeFields validates the requested fields; an empty list means ALL
// fields.
//
// The id field is ADDED to the list even when it is not requested: Query joins
// over [query.IDField] and a record without an id would have ended in
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

// splitFilters separates the filters into an id set and a filter.
//
// If there is no id filter, a nil id slice is returned. An empty slice carries
// a meaning SEPARATE from nil: it means "no id at all" and an empty result is
// returned.
func splitFilters(filters map[string]any) ([]string, models.CustomerFilter, error) {
	var (
		ids    []string
		filter models.CustomerFilter
	)
	if len(filters) == 0 {
		return nil, filter, nil
	}

	for name, value := range filters {
		switch name {
		case filterID:
			parsed, err := stringSet(name, value)
			if err != nil {
				return nil, filter, err
			}
			ids = parsed
		case filterEmail:
			raw, err := stringValue(name, value)
			if err != nil {
				return nil, filter, err
			}
			normalized, err := normalizeEmail(raw)
			if err != nil {
				return nil, filter, err
			}
			filter.Email = &normalized
		case filterHasAccount:
			flag, ok := value.(bool)
			if !ok {
				return nil, filter, errors.Invalid(CodeInvalidInput,
					"filter %q has to be a boolean, %T given", name, value)
			}
			filter.HasAccount = &flag
		case filterGroupID:
			raw, err := stringValue(name, value)
			if err != nil {
				return nil, filter, err
			}
			if err := requireID(raw, models.CustomerGroupIDPrefix, "group id"); err != nil {
				return nil, filter, err
			}
			filter.GroupID = &raw
		default:
			return nil, filter, errors.Invalid(CodeInvalidInput,
				"filter %q is not supported by the %s provider (supported: %v)",
				name, Entity, supportedFilters)
		}
	}

	if ids != nil && len(filters) > 1 {
		return nil, filter, errors.Invalid(CodeInvalidInput,
			"filter %q cannot be used together with other filters", filterID)
	}
	return ids, filter, nil
}

// stringSet converts a filter value into an id set.
func stringSet(name string, value any) ([]string, error) {
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
			"filter %q has to be a string or a string slice, %T given", name, value)
	}
}

// stringValue converts a filter value into a single string.
func stringValue(name string, value any) (string, error) {
	typed, ok := value.(string)
	if !ok {
		return "", errors.Invalid(CodeInvalidInput,
			"filter %q has to be a string, %T given", name, value)
	}
	return typed, nil
}

// clampToInt64 carries Query's int pagination value onto the service's int64
// surface.
//
// The conversion is lossless on every platform: int is at most 64 bits. A
// negative value is passed through without correction so that normalizePaging
// REJECTS it — silently pulling it to zero would hide the client's faulty
// request.
func clampToInt64(n int) int64 {
	return int64(n)
}
