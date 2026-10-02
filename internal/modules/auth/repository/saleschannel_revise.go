package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository/authdb"
)

// ReviseSalesChannel writes the channel's terms only while they are the ones
// read, and reports whether it wrote (ADR 0352). A name another live channel
// holds is refused as the update's is.
func (r *Repo) ReviseSalesChannel(
	ctx context.Context, id string, read, next models.ChannelTerms, now time.Time,
) (models.SalesChannel, bool, error) {
	if err := r.ready(); err != nil {
		return models.SalesChannel{}, false, err
	}

	row, err := r.q.ReviseSalesChannel(ctx, authdb.ReviseSalesChannelParams{
		Name: next.Name, Description: next.Description, IsDisabled: next.IsDisabled, UpdatedAt: fromTime(now),
		ID: id, ReadName: read.Name, ReadDescription: read.Description, ReadIsDisabled: read.IsDisabled,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return models.SalesChannel{}, false, nil
	case err != nil:
		return models.SalesChannel{}, false, classifyChannelWrite(err, next.Name, "could not revise sales channel")
	}
	channel, err := toSalesChannel(row)

	return channel, err == nil, err
}
