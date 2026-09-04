package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// Entity is the entity name auth opens to the Query layer.
//
// Even though the module's name is "auth", the entity is "sales_channel":
// Query finds the target of an extension FROM the ENTITY name, and this is the
// name that will stand at the end of link definitions (e.g. product ↔
// sales_channel). The provider is registered in the container under the name
// "sales_channel" + query.ProviderSuffix.
//
// Users and API keys ARE NOT OPENED to Query: both of them are identity data,
// and putting them on a cross-module read surface would have meant that one
// day an extension adds the admin list to a storefront response.
const Entity = "sales_channel"

// The field names the provider offers.
const (
	fieldID          = "id"
	fieldName        = "name"
	fieldDescription = "description"
	fieldIsDisabled  = "is_disabled"
	fieldMetadata    = "metadata"
	fieldCreatedAt   = "created_at"
	fieldUpdatedAt   = "updated_at"
)

// The filter names the provider recognizes.
const (
	filterID         = "id"
	filterName       = "name"
	filterIsDisabled = "is_disabled"
)

// supportedFields are the fields the provider recognizes; if another field is
// requested errors.Invalid is returned (ADR 0004: field validation belongs to
// the provider).
var supportedFields = []string{
	fieldID, fieldName, fieldDescription, fieldIsDisabled,
	fieldMetadata, fieldCreatedAt, fieldUpdatedAt,
}

// supportedFilters are the filters the provider recognizes.
var supportedFilters = []string{filterID, filterName, filterIsDisabled}

// QueryProvider opens sales channels to the Query layer (ADR 0004).
//
// The interface is defined in internal/core/query; this type only satisfies
// the signature and tells the core nothing (the provider side of ADR 0001).
type QueryProvider struct {
	svc *Service
}

var _ query.Provider = (*QueryProvider)(nil)

// NewQueryProvider produces a provider that runs on the given service.
func NewQueryProvider(svc *Service) *QueryProvider {
	return &QueryProvider{svc: svc}
}

// Entity returns the entity name the provider offers.
func (p *QueryProvider) Entity() string { return Entity }

// List returns the root sales channel records.
//
// Supported filters: "id" (a string or a string slice), "name", "is_disabled".
// The "id" filter CANNOT BE COMBINED WITH THE OTHERS — when an exact set of
// identifiers has already been named, a second filter would have meant the
// record the caller asked for being silently eliminated, and the result would
// have come back empty.
//
// If a limit of zero is given, the module's default page size is applied
// INSTEAD OF the "unlimited" of the Query contract.
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
		// If there is an identifier filter no paging is applied: the caller
		// has already named an exact set.
		return p.fetch(ctx, ids, fields)
	}

	limit, offset, err := normalizePaging(int64(opts.Limit), int64(opts.Offset))
	if err != nil {
		return nil, err
	}

	channels, _, err := p.svc.repo.ListSalesChannels(ctx, filter, limit, offset)
	if err != nil {
		return nil, err
	}
	return records(channels, fields), nil
}

// FetchByIDs returns the records corresponding to the given identifiers in a
// SINGLE round trip.
//
// No record is returned for an identifier that is not found; this is not an
// error (ADR 0004).
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

// fetch reads the set of identifiers and converts it into records.
func (p *QueryProvider) fetch(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	if len(ids) == 0 {
		return []query.Record{}, nil
	}

	channels, err := p.svc.repo.GetSalesChannelsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return records(channels, fields), nil
}

// records converts sales channels into Query records.
func records(channels []models.SalesChannel, fields []string) []query.Record {
	out := make([]query.Record, 0, len(channels))
	for i := range channels {
		c := &channels[i]
		record := make(query.Record, len(fields))
		for _, field := range fields {
			switch field {
			case fieldID:
				record[fieldID] = c.ID
			case fieldName:
				record[fieldName] = c.Name
			case fieldDescription:
				record[fieldDescription] = c.Description
			case fieldIsDisabled:
				record[fieldIsDisabled] = c.IsDisabled
			case fieldMetadata:
				record[fieldMetadata] = c.Metadata
			case fieldCreatedAt:
				record[fieldCreatedAt] = c.CreatedAt
			case fieldUpdatedAt:
				record[fieldUpdatedAt] = c.UpdatedAt
			}
		}
		out = append(out, record)
	}
	return out
}

// normalizeFields validates the requested fields; an empty list means ALL
// fields.
//
// The identifier field is ADDED to the list even when it was not requested:
// Query merges the records over [query.IDField] and a record without an
// identifier would have ended in errors.KindInternal.
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

// splitFilters separates the filters into a set of identifiers and a filter.
//
// If there is no identifier filter a nil identifier slice is returned. An empty
// slice carries a meaning SEPARATE from nil: it means "no identifiers" and an
// empty result is returned.
func splitFilters(filters map[string]any) ([]string, models.SalesChannelFilter, error) {
	var (
		ids    []string
		filter models.SalesChannelFilter
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
		case filterName:
			raw, err := stringValue(name, value)
			if err != nil {
				return nil, filter, err
			}
			filter.Name = &raw
		case filterIsDisabled:
			flag, ok := value.(bool)
			if !ok {
				return nil, filter, errors.Invalid(CodeInvalidInput,
					"filter %q has to be a boolean, %T given", name, value)
			}
			filter.IsDisabled = &flag
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

// stringSet converts a filter value into a set of identifiers.
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
