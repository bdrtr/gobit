package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// EntityGroup is the entity name the customer groups are opened to the Query
// layer under (ADR 0321). The provider is registered in the container under
// the name "customer_group" + query.ProviderSuffix.
//
// The groups were reachable only as ids on a customer's record, so a screen
// that names a group, or offers the groups to choose from, had no read: the
// admin panel reaches the modules only through Query (ADR 0011), and the
// group endpoints are HTTP.
const EntityGroup = "customer_group"

// The fields the group provider offers.
const (
	fieldGroupName = "name"
	fieldGroupRank = "rank"
)

// groupFields are the fields the group provider recognizes; another one is
// refused (ADR 0004).
var groupFields = []string{fieldID, fieldGroupName, fieldGroupRank, fieldCreatedAt, fieldUpdatedAt}

// GroupProvider opens the customer groups to the Query layer (ADR 0321): their
// id, name and rank, newest first, or the groups of an id set. Who belongs to
// a group is the customer entity's group_ids and its group_id filter; two
// entities answering one question is how two answers start to disagree.
type GroupProvider struct {
	svc *Service
}

var _ query.Provider = (*GroupProvider)(nil)

// NewGroupProvider builds the provider of the "customer_group" entity.
func NewGroupProvider(svc *Service) *GroupProvider { return &GroupProvider{svc: svc} }

// Entity returns the entity name the provider offers.
func (p *GroupProvider) Entity() string { return EntityGroup }

// List returns the live groups, newest first, a page at a time; the only
// filter is "id", which names an exact set and so takes no page. A zero limit
// is the module's default page rather than every group, as for the customers.
func (p *GroupProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := p.svc.ready(); err != nil {
		return nil, err
	}
	fields, err := groupFieldsOf(opts.Fields)
	if err != nil {
		return nil, err
	}

	// Every name is checked before the id set is read: a map is walked in no
	// order, and an id filter met first would answer before an unsupported
	// filter beside it was refused.
	for name := range opts.Filters {
		if name != filterID {
			return nil, errors.Invalid(CodeInvalidInput,
				"filter %q is not supported by the %s provider (supported: [%s])", name, EntityGroup, filterID)
		}
	}
	if value, named := opts.Filters[filterID]; named {
		ids, err := stringSet(filterID, value)
		if err != nil {
			return nil, err
		}
		return p.fetch(ctx, ids, fields)
	}

	limit, offset, err := normalizePaging(clampToInt64(opts.Limit), clampToInt64(opts.Offset))
	if err != nil {
		return nil, err
	}
	groups, _, err := p.svc.repo.ListGroups(ctx, limit, offset)
	if err != nil {
		return nil, err
	}

	return groupRecords(groups, fields), nil
}

// FetchByIDs returns the live groups of the ids in one round; an id that
// names none returns nothing and is not an error (ADR 0004).
func (p *GroupProvider) FetchByIDs(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	if err := p.svc.ready(); err != nil {
		return nil, err
	}
	normalized, err := groupFieldsOf(fields)
	if err != nil {
		return nil, err
	}

	return p.fetch(ctx, ids, normalized)
}

// fetch reads the id set into records.
func (p *GroupProvider) fetch(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	if len(ids) == 0 {
		return []query.Record{}, nil
	}
	groups, err := p.svc.repo.GetGroupsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	return groupRecords(groups, fields), nil
}

// groupFieldsOf checks the requested fields, every one when none is named,
// and adds the id Query joins over.
func groupFieldsOf(fields []string) ([]string, error) {
	if len(fields) == 0 {
		return slices.Clone(groupFields), nil
	}
	out := make([]string, 0, len(fields)+1)
	for _, field := range fields {
		if !slices.Contains(groupFields, field) {
			return nil, errors.Invalid(CodeInvalidInput,
				"field %q does not exist in the %s provider (supported: %v)", field, EntityGroup, groupFields)
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

// groupRecords converts the groups into records of the fields.
func groupRecords(groups []models.CustomerGroup, fields []string) []query.Record {
	records := make([]query.Record, 0, len(groups))
	for i := range groups {
		g := &groups[i]
		record := make(query.Record, len(fields))
		for _, field := range fields {
			switch field {
			case fieldID:
				record[fieldID] = g.ID
			case fieldGroupName:
				record[fieldGroupName] = g.Name
			case fieldGroupRank:
				record[fieldGroupRank] = g.Rank
			case fieldCreatedAt:
				record[fieldCreatedAt] = g.CreatedAt
			case fieldUpdatedAt:
				record[fieldUpdatedAt] = g.UpdatedAt
			}
		}
		records = append(records, record)
	}

	return records
}
