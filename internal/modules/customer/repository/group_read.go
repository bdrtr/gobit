package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// GetGroupsByIDs returns the live groups of the ids in one round, ordered by
// id; an id that names no live group returns nothing and is not an error (ADR
// 0321).
func (r *Repo) GetGroupsByIDs(ctx context.Context, ids []string) ([]models.CustomerGroup, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.CustomerGroup{}, nil
	}

	rows, err := r.q.GetCustomerGroupsByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "the customer groups could not be read")
	}

	return toGroups(rows)
}
