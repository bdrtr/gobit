package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// ReviseCampaign writes the campaign's name, description, window and budget
// limit while they are still the ones the caller read, and reports whether it
// did (ADR 0331). A campaign that moved since, that is not there, or whose
// budget type does not take the limit given, is left as it is; the caller
// tells which from the campaign as it is.
func (r *Repo) ReviseCampaign(
	ctx context.Context, id string, read, next models.CampaignTerms, now time.Time,
) (models.Campaign, bool, error) {
	if err := r.ready(); err != nil {
		return models.Campaign{}, false, err
	}

	row, err := r.q.ReviseCampaign(ctx, promotiondb.ReviseCampaignParams{
		ID: id, Name: next.Name, Description: next.Description,
		StartsAt: fromTimePtr(next.StartsAt), EndsAt: fromTimePtr(next.EndsAt),
		BudgetLimit: copyInt64(next.BudgetLimit), UpdatedAt: fromTime(now),
		ReadName: read.Name, ReadDescription: read.Description,
		ReadStartsAt: fromTimePtr(read.StartsAt), ReadEndsAt: fromTimePtr(read.EndsAt),
		ReadBudgetLimit: copyInt64(read.BudgetLimit),
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return models.Campaign{}, false, nil
	case err != nil:
		return models.Campaign{}, false, wrapDB(err, "the campaign %s could not be revised", id)
	}

	return toCampaign(row), true, nil
}
