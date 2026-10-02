package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// ReviseMethodValue writes the discount's value while the promotion's method
// is still of the type and the value the caller read, and reports whether it
// did (ADR 0338); a method that moved since, or that is not there, is left
// as it is.
func (r *Repo) ReviseMethodValue(
	ctx context.Context, promotionID string, readType models.ApplicationMethodType, readValue, value int64,
	now time.Time,
) (models.ApplicationMethod, bool, error) {
	if err := r.ready(); err != nil {
		return models.ApplicationMethod{}, false, err
	}

	row, err := r.q.ReviseMethodValue(ctx, promotiondb.ReviseMethodValueParams{
		PromotionID: promotionID, Value: value, UpdatedAt: fromTime(now),
		ReadType: string(readType), ReadValue: readValue,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return models.ApplicationMethod{}, false, nil
	case err != nil:
		return models.ApplicationMethod{}, false, wrapDB(err, "the discount of promotion %s could not be revised", promotionID)
	}

	return toApplicationMethod(row), true, nil
}
