package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository/customerdb"
)

// CodeGroupNameTaken reports that the group name is already in use.
const CodeGroupNameTaken = "customer_group_name_taken"

// CreateGroup writes a new customer group.
func (r *Repo) CreateGroup(ctx context.Context, g models.CustomerGroup) (models.CustomerGroup, error) {
	if err := r.ready(); err != nil {
		return models.CustomerGroup{}, err
	}

	meta, err := fromMetadata(g.Metadata)
	if err != nil {
		return models.CustomerGroup{}, err
	}

	row, err := r.q.InsertCustomerGroup(ctx, customerdb.InsertCustomerGroupParams{
		ID:        g.ID,
		Name:      g.Name,
		Rank:      g.Rank,
		Metadata:  meta,
		CreatedAt: fromTime(g.CreatedAt),
	})
	if err != nil {
		if ConstraintName(err) == IndexGroupName {
			return models.CustomerGroup{}, errors.Wrap(err, errors.KindConflict, CodeGroupNameTaken,
				"a customer group named %q already exists", g.Name)
		}
		return models.CustomerGroup{}, wrapDB(err, "the customer group could not be created")
	}
	return toGroup(row)
}

// GetGroup returns the group by id; errors.NotFound if it does not exist.
func (r *Repo) GetGroup(ctx context.Context, id string) (models.CustomerGroup, error) {
	if err := r.ready(); err != nil {
		return models.CustomerGroup{}, err
	}

	row, err := r.q.GetCustomerGroup(ctx, id)
	if err != nil {
		return models.CustomerGroup{}, notFoundOr(err, CodeGroupNotFound, "customer group not found: %s", id)
	}
	return toGroup(row)
}

// ListGroups returns the paginated list of groups and the TOTAL record count.
func (r *Repo) ListGroups(ctx context.Context, limit, offset int64) ([]models.CustomerGroup, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListCustomerGroups(ctx, customerdb.ListCustomerGroupsParams{
		Lim: toInt32(limit),
		Off: toInt32(offset),
	})
	if err != nil {
		return nil, 0, wrapDB(err, "the customer groups could not be read")
	}

	total, err := r.q.CountCustomerGroups(ctx)
	if err != nil {
		return nil, 0, wrapDB(err, "the customer groups could not be counted")
	}

	groups, err := toGroups(rows)
	if err != nil {
		return nil, 0, err
	}
	return groups, total, nil
}

// UpdateGroup updates the given fields of the group; errors.NotFound if it does
// not exist.
//
// A new name already used by another LIVE group returns errors.Conflict; the
// rule lives in the partial unique index in the database (see [IndexGroupName])
// and is not repeated on the application side.
func (r *Repo) UpdateGroup(
	ctx context.Context,
	id string,
	patch models.CustomerGroupPatch,
	now time.Time,
) (models.CustomerGroup, error) {
	if err := r.ready(); err != nil {
		return models.CustomerGroup{}, err
	}

	meta, err := patchMetadata(patch.Metadata)
	if err != nil {
		return models.CustomerGroup{}, err
	}

	row, err := r.q.UpdateCustomerGroup(ctx, customerdb.UpdateCustomerGroupParams{
		ID:   id,
		Name: patch.Name,
		// nil leaves the order the merchant set alone; the column is NOT NULL and
		// only the ARGUMENT is nullable (ADR 0049).
		Rank:      patch.Rank,
		Metadata:  meta,
		UpdatedAt: fromTime(now),
	})
	if err != nil {
		if ConstraintName(err) == IndexGroupName {
			return models.CustomerGroup{}, errors.Wrap(err, errors.KindConflict, CodeGroupNameTaken,
				"a customer group with this name already exists")
		}
		return models.CustomerGroup{}, notFoundOr(err, CodeGroupNotFound,
			"customer group not found: %s", id)
	}
	return toGroup(row)
}

// DeleteGroup soft-deletes the group; errors.NotFound if it does not exist.
//
// The membership rows are LEFT BEHIND, and deliberately so: every query that
// reads a group filters on deleted_at IS NULL, so a deleted group shows up
// neither among the customer's groups nor in the group-filtered customer
// listing. The rows go by cascade when the record is one day really deleted.
func (r *Repo) DeleteGroup(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.q.SoftDeleteCustomerGroup(ctx, customerdb.SoftDeleteCustomerGroupParams{
		ID:        id,
		DeletedAt: fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodeGroupNotFound, "customer group not found: %s", id)
	}
	return nil
}

// AddToGroup adds the customer to the group; if the membership already exists
// it does nothing.
//
// The existence of the customer and of the group is checked in the SAME
// transaction, before the membership is written: a foreign key violation would
// give the same result, but it would not say which side (the customer or the
// group) is missing, and it would reach the client as a 422; for a missing
// resource the right class is errors.NotFound.
//
// # "In the same transaction" is NOT PROTECTION here
//
// The checks take no lock, and deliberately. A transaction, on its own, under
// READ COMMITTED protects nothing: every statement takes a fresh snapshot, so
// if a [Repo.DeleteCustomer] or a [Repo.DeleteGroup] slips in and commits after
// the check, the membership is still written and the foreign key does not
// object, because the delete is SOFT. This is the very shape that costs money
// in the tax module — but its CONSEQUENCE here is zero, and the difference was
// measured (2026-09-06): the membership rows of a deleted group are already
// LEFT BEHIND ([Repo.DeleteGroup]) and so are those of a deleted customer
// ([Repo.DeleteCustomer]), because every query that reads a group or a
// customer filters on deleted_at IS NULL. The row the race produces CANNOT BE
// TOLD APART from the row the module already produces in normal operation.
//
// Adding a lock, therefore, closes no observable difference; if one is added,
// it must be added with [customerdb.Queries.GetCustomerForUpdate], because in
// this module the customer row is ALWAYS locked first (see
// queries/customer.sql).
func (r *Repo) AddToGroup(ctx context.Context, customerID, groupID string, now time.Time) error {
	return r.inTx(ctx, func(q *customerdb.Queries) error {
		if _, err := q.GetCustomer(ctx, customerID); err != nil {
			return notFoundOr(err, CodeCustomerNotFound, "customer not found: %s", customerID)
		}
		group, err := q.GetCustomerGroup(ctx, groupID)
		if err != nil {
			return notFoundOr(err, CodeGroupNotFound, "customer group not found: %s", groupID)
		}
		if group.Segment != nil {
			return segmentManaged(groupID)
		}

		if err := q.AddCustomerToGroup(ctx, customerdb.AddCustomerToGroupParams{
			CustomerID:      customerID,
			CustomerGroupID: groupID,
			CreatedAt:       fromTime(now),
		}); err != nil {
			return wrapDB(err, "the customer could not be added to the group")
		}
		return nil
	})
}

// RemoveFromGroup removes the customer from the group; errors.NotFound if there
// is no membership.
//
// Without the count of deleted rows this distinction could not be made: DELETE
// also returns without an error when it touches no row, and the caller would
// believe it had removed a membership that never existed.
func (r *Repo) RemoveFromGroup(ctx context.Context, customerID, groupID string) error {
	if err := r.ready(); err != nil {
		return err
	}

	// A group that is not found falls through: the missing membership is what
	// the caller hears, as before.
	if group, err := r.q.GetCustomerGroup(ctx, groupID); err == nil && group.Segment != nil {
		return segmentManaged(groupID)
	}
	affected, err := r.q.RemoveCustomerFromGroup(ctx, customerdb.RemoveCustomerFromGroupParams{
		CustomerID:      customerID,
		CustomerGroupID: groupID,
	})
	if err != nil {
		return wrapDB(err, "the customer could not be removed from the group")
	}
	if affected == 0 {
		return errors.NotFound(CodeMembershipNotFound,
			"customer %s is not a member of group %s", customerID, groupID)
	}
	return nil
}

// ListGroupsOf returns the groups the customer is a member of.
func (r *Repo) ListGroupsOf(ctx context.Context, customerID string) ([]models.CustomerGroup, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListGroupsOfCustomer(ctx, customerID)
	if err != nil {
		return nil, wrapDB(err, "the customer's groups could not be read: %s", customerID)
	}
	return toGroups(rows)
}

// GroupIDsOfCustomers returns the group ids of several customers in ONE query.
//
// The result is a map from customer id to group ids. A customer with no group
// has NO KEY; the caller can use the nil slice as an empty slice. The Query
// provider calls this as a batch and runs no separate query per customer
// (ADR 0004's N+1 ban).
func (r *Repo) GroupIDsOfCustomers(ctx context.Context, customerIDs []string) (map[string][]string, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(customerIDs) == 0 {
		return map[string][]string{}, nil
	}

	rows, err := r.q.ListGroupIDsOfCustomers(ctx, customerIDs)
	if err != nil {
		return nil, wrapDB(err, "the customers' group ids could not be read")
	}

	out := make(map[string][]string, len(customerIDs))
	for _, row := range rows {
		out[row.CustomerID] = append(out[row.CustomerID], row.CustomerGroupID)
	}
	return out, nil
}

// toGroup converts a group row into the domain model.
func toGroup(row customerdb.CustomerGroup) (models.CustomerGroup, error) {
	meta, err := toMetadata(row.Metadata)
	if err != nil {
		return models.CustomerGroup{}, err
	}
	var segment *models.SegmentRule
	if row.Segment != nil {
		segment = &models.SegmentRule{}
		if err := json.Unmarshal(row.Segment, segment); err != nil {
			return models.CustomerGroup{}, errors.Wrap(err, errors.KindInternal, CodeGroupNotFound,
				"the segment rule of group %s could not be read", row.ID)
		}
	}
	return models.CustomerGroup{
		ID:                 row.ID,
		Name:               row.Name,
		Rank:               row.Rank,
		Metadata:           meta,
		Segment:            segment,
		SegmentSetAt:       toTimePtr(row.SegmentSetAt),
		SegmentEvaluatedAt: toTimePtr(row.SegmentEvaluatedAt),
		CreatedAt:          toTime(row.CreatedAt),
		UpdatedAt:          toTime(row.UpdatedAt),
		DeletedAt:          toTimePtr(row.DeletedAt),
	}, nil
}

// toGroups converts a slice of group rows into domain models.
func toGroups(rows []customerdb.CustomerGroup) ([]models.CustomerGroup, error) {
	out := make([]models.CustomerGroup, 0, len(rows))
	for i := range rows {
		g, err := toGroup(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}
