package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// GroupInput is the write input of a customer group.
type GroupInput struct {
	// Name is the group's display name; it is required and unique among live
	// groups.
	Name string
	// Rank is the order over groups; the SMALLER value wins, the default is 0.
	//
	// It decides which group speaks for a customer who belongs to several
	// groups: the cart writes the highest-ranked group into the pricing rule
	// context (ADR 0049).
	Rank int32
	// Metadata is free structural context; it can be empty.
	Metadata map[string]any
}

// CreateGroup creates a new customer group.
//
// A live group with the same name returns errors.Conflict; the rule lives in
// the partial unique index in the database.
func (s *Service) CreateGroup(ctx context.Context, in GroupInput) (models.CustomerGroup, error) {
	if err := s.ready(); err != nil {
		return models.CustomerGroup{}, err
	}
	if err := requireText("group name", in.Name); err != nil {
		return models.CustomerGroup{}, err
	}
	name := strings.TrimSpace(in.Name)
	if err := checkLen("group name", name, models.MaxNameLen); err != nil {
		return models.CustomerGroup{}, err
	}

	now := s.clock()
	return s.repo.CreateGroup(ctx, models.CustomerGroup{
		ID:   models.NewCustomerGroupID(now),
		Name: name,
		// Rank is NOT validated: every int32 is a legal order, negative included,
		// so that a merchant who has ranked three groups 0, 1, 2 can put a fourth
		// in front without renumbering the others (ADR 0049).
		Rank:      in.Rank,
		Metadata:  in.Metadata,
		CreatedAt: now,
	})
}

// UpdateGroupInput is the partial update input of a customer group.
//
// A nil field means "leave it alone", a set field means "write this value".
type UpdateGroupInput struct {
	// Name is the group's new name; if given it cannot be empty, and it is
	// unique among live groups.
	Name *string
	// Rank is the new order; if nil it is NOT TOUCHED.
	//
	// Being a pointer keeps the order the merchant set from being reset to zero
	// silently by a correction of the name: "not given" and "set to zero" are
	// different things (ADR 0049).
	Rank *int32
	// Metadata is the new metadata map; it replaces the whole column.
	Metadata map[string]any
}

// UpdateGroup updates the given fields of the group; errors.NotFound if it does
// not exist.
//
// Another live group with the same name returns errors.Conflict. A name, if
// given, CANNOT BE EMPTY: a partial update can skip a field but cannot lift a
// requirement that is already there.
func (s *Service) UpdateGroup(ctx context.Context, id string, in UpdateGroupInput) (models.CustomerGroup, error) {
	if err := s.ready(); err != nil {
		return models.CustomerGroup{}, err
	}
	if err := requireID(id, models.CustomerGroupIDPrefix, "group id"); err != nil {
		return models.CustomerGroup{}, err
	}

	patch := models.CustomerGroupPatch{Rank: in.Rank, Metadata: in.Metadata}
	if in.Name != nil {
		if err := requireText("group name", *in.Name); err != nil {
			return models.CustomerGroup{}, err
		}
		name := strings.TrimSpace(*in.Name)
		if err := checkLen("group name", name, models.MaxNameLen); err != nil {
			return models.CustomerGroup{}, err
		}
		patch.Name = &name
	}

	return s.repo.UpdateGroup(ctx, id, patch, s.clock())
}

// DeleteGroup soft-deletes the group; errors.NotFound if it does not exist.
//
// The membership rows are NOT REMOVED, but the deleted group appears in no
// read: [Service.ListGroups], [Service.GetGroup], [Service.ListGroupsOf], the
// Query provider's group ids and the group-filtered customer listing all SKIP
// the deleted group. The group's name is freed as well; the uniqueness index
// covers only live groups.
//
// The delete changes the members' price segment: this group's id is no longer
// carried into pricing's rule context.
func (s *Service) DeleteGroup(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.CustomerGroupIDPrefix, "group id"); err != nil {
		return err
	}

	if err := s.repo.DeleteGroup(ctx, id, s.clock()); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "customer group deleted",
		slog.String("customer_group_id", id),
	)
	return nil
}

// GetGroup returns the group by id; errors.NotFound if it does not exist.
func (s *Service) GetGroup(ctx context.Context, id string) (models.CustomerGroup, error) {
	if err := s.ready(); err != nil {
		return models.CustomerGroup{}, err
	}
	if err := requireID(id, models.CustomerGroupIDPrefix, "group id"); err != nil {
		return models.CustomerGroup{}, err
	}
	return s.repo.GetGroup(ctx, id)
}

// ListGroups returns the paginated list of groups.
func (s *Service) ListGroups(ctx context.Context, limit, offset int64) (Page[models.CustomerGroup], error) {
	if err := s.ready(); err != nil {
		return Page[models.CustomerGroup]{}, err
	}
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return Page[models.CustomerGroup]{}, err
	}

	items, total, err := s.repo.ListGroups(ctx, limit, offset)
	if err != nil {
		return Page[models.CustomerGroup]{}, err
	}
	return Page[models.CustomerGroup]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// AddToGroup adds the customer to the group.
//
// The operation is idempotent: a second call for a customer who is already a
// member returns no error, because membership is a set and a repeat of the same
// call (a retry, a double click) must give the same result. A customer or group
// that does not exist returns errors.NotFound.
func (s *Service) AddToGroup(ctx context.Context, customerID, groupID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(customerID, models.CustomerIDPrefix, "customer id"); err != nil {
		return err
	}
	if err := requireID(groupID, models.CustomerGroupIDPrefix, "group id"); err != nil {
		return err
	}

	if err := s.repo.AddToGroup(ctx, customerID, groupID, s.clock()); err != nil {
		return err
	}

	s.log.DebugContext(ctx, "customer added to group",
		slog.String("customer_id", customerID),
		slog.String("customer_group_id", groupID),
	)
	return nil
}

// RemoveFromGroup removes the customer from the group; errors.NotFound if there
// is no membership.
//
// Adding is idempotent, removing is not. The difference is deliberate: removing
// a membership that does not exist is the most common sign that the client
// called with the wrong id, and returning success silently would hide that
// mistake.
func (s *Service) RemoveFromGroup(ctx context.Context, customerID, groupID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(customerID, models.CustomerIDPrefix, "customer id"); err != nil {
		return err
	}
	if err := requireID(groupID, models.CustomerGroupIDPrefix, "group id"); err != nil {
		return err
	}
	return s.repo.RemoveFromGroup(ctx, customerID, groupID)
}

// ListGroupsOf returns the groups the customer is a member of.
//
// The customer's existence is checked FIRST: if an empty list came back for a
// customer that does not exist, the client would take it for "has no groups"
// instead of a 404.
func (s *Service) ListGroupsOf(ctx context.Context, customerID string) ([]models.CustomerGroup, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(customerID, models.CustomerIDPrefix, "customer id"); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetCustomer(ctx, customerID); err != nil {
		return nil, err
	}
	return s.repo.ListGroupsOf(ctx, customerID)
}
