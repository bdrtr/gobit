package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository/authdb"
)

// CreateSalesChannel writes a new sales channel.
//
// If the name is already in use, errors.Conflict is returned; the rule lives in
// the partial unique index in the database (see [IndexChannelName]).
func (r *Repo) CreateSalesChannel(ctx context.Context, c models.SalesChannel) (models.SalesChannel, error) {
	if err := r.ready(); err != nil {
		return models.SalesChannel{}, err
	}

	meta, err := fromMetadata(c.Metadata)
	if err != nil {
		return models.SalesChannel{}, err
	}

	row, err := r.q.InsertSalesChannel(ctx, authdb.InsertSalesChannelParams{
		ID:          c.ID,
		Name:        c.Name,
		Description: c.Description,
		IsDisabled:  c.IsDisabled,
		Metadata:    meta,
		CreatedAt:   fromTime(c.CreatedAt),
	})
	if err != nil {
		return models.SalesChannel{}, classifyChannelWrite(err, c.Name, "could not create sales channel")
	}
	return toSalesChannel(row)
}

// GetSalesChannel returns the channel by id; errors.NotFound when there is none.
func (r *Repo) GetSalesChannel(ctx context.Context, id string) (models.SalesChannel, error) {
	if err := r.ready(); err != nil {
		return models.SalesChannel{}, err
	}

	row, err := r.q.GetSalesChannel(ctx, id)
	if err != nil {
		return models.SalesChannel{}, notFoundOr(err, CodeSalesChannelNotFound,
			"sales channel not found: %s", id)
	}
	return toSalesChannel(row)
}

// ListSalesChannels returns the filtered and paginated channel list together
// with the TOTAL number of records matching the filter.
func (r *Repo) ListSalesChannels(
	ctx context.Context,
	filter models.SalesChannelFilter,
	limit, offset int64,
) ([]models.SalesChannel, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListSalesChannels(ctx, authdb.ListSalesChannelsParams{
		Name:       filter.Name,
		IsDisabled: filter.IsDisabled,
		Lim:        toInt32(limit),
		Off:        toInt32(offset),
	})
	if err != nil {
		return nil, 0, wrapDB(err, "could not read the sales channel list")
	}

	total, err := r.q.CountSalesChannels(ctx, authdb.CountSalesChannelsParams{
		Name:       filter.Name,
		IsDisabled: filter.IsDisabled,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "could not read the sales channel count")
	}

	channels, err := toSalesChannels(rows)
	if err != nil {
		return nil, 0, err
	}
	return channels, total, nil
}

// GetSalesChannelsByIDs returns the channels matching the given ids in a SINGLE
// query. No record is returned for an id that is not found; that is not an
// error.
func (r *Repo) GetSalesChannelsByIDs(ctx context.Context, ids []string) ([]models.SalesChannel, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.SalesChannel{}, nil
	}

	rows, err := r.q.ListSalesChannelsByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "could not read the sales channels")
	}
	return toSalesChannels(rows)
}

// UpdateSalesChannel updates the given fields of the channel.
func (r *Repo) UpdateSalesChannel(
	ctx context.Context,
	id string,
	patch models.SalesChannelPatch,
	now time.Time,
) (models.SalesChannel, error) {
	if err := r.ready(); err != nil {
		return models.SalesChannel{}, err
	}

	meta, err := patchMetadata(patch.Metadata)
	if err != nil {
		return models.SalesChannel{}, err
	}

	row, err := r.q.UpdateSalesChannel(ctx, authdb.UpdateSalesChannelParams{
		Name:        patch.Name,
		Description: patch.Description,
		IsDisabled:  patch.IsDisabled,
		Metadata:    meta,
		UpdatedAt:   fromTime(now),
		ID:          id,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.SalesChannel{}, errors.NotFound(CodeSalesChannelNotFound,
				"sales channel not found: %s", id)
		}
		return models.SalesChannel{}, classifyChannelWrite(err, derefOr(patch.Name),
			"could not update sales channel")
	}
	return toSalesChannel(row)
}

// DeleteSalesChannel soft-deletes the channel and removes the key links.
//
// The links are deleted in the SAME transaction: because a soft delete is an
// UPDATE, the foreign key CASCADE does not engage and publishable keys would
// stay linked to a deleted channel.
func (r *Repo) DeleteSalesChannel(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	return r.inTx(ctx, func(q *authdb.Queries) error {
		if _, err := q.SoftDeleteSalesChannel(ctx, authdb.SoftDeleteSalesChannelParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodeSalesChannelNotFound, "sales channel not found: %s", id)
		}
		if err := q.DeleteLinksOfSalesChannel(ctx, id); err != nil {
			return wrapDB(err, "could not delete the key links of the sales channel")
		}
		return nil
	})
}

// classifyChannelWrite classifies a write error with respect to name conflicts.
func classifyChannelWrite(err error, name, message string) error {
	if err == nil {
		return nil
	}
	if ConstraintName(err) == IndexChannelName {
		return errors.Wrap(err, errors.KindConflict, CodeChannelNameTaken,
			"a sales channel named %q already exists", name)
	}
	return wrapDB(err, "%s", message)
}
