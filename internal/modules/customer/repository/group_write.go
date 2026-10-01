package repository

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository/customerdb"
)

// ReviseGroup writes the group's name and rank while they are still the ones
// the caller read, and reports whether it did; a group that moved since, or
// that is not there, is left as it is (ADR 0329). A name another live group
// holds is a conflict.
func (r *Repo) ReviseGroup(
	ctx context.Context, id, readName string, readRank int32, name string, rank int32, now time.Time,
) (models.CustomerGroup, bool, error) {
	if err := r.ready(); err != nil {
		return models.CustomerGroup{}, false, err
	}

	row, err := r.q.ReviseCustomerGroup(ctx, customerdb.ReviseCustomerGroupParams{
		ID: id, ReadName: readName, ReadRank: readRank, Name: name, Rank: rank, UpdatedAt: fromTime(now),
	})
	switch {
	case stderrors.Is(err, pgx.ErrNoRows):
		return models.CustomerGroup{}, false, nil
	case ConstraintName(err) == IndexGroupName:
		return models.CustomerGroup{}, false, errors.Wrap(err, errors.KindConflict, CodeGroupNameTaken,
			"a customer group named %q already exists", name)
	case err != nil:
		return models.CustomerGroup{}, false, wrapDB(err, "customer group %s could not be revised", id)
	}
	group, err := toGroup(row)

	return group, err == nil, err
}
