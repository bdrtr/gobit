package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/repository/taxdb"
)

// ReviseTaxRate writes a rate's name and rate only while they are the ones
// read, and reports whether it wrote (ADR 0378).
func (r *Repo) ReviseTaxRate(
	ctx context.Context, id string, read, next models.TaxRateTerms, now time.Time,
) (models.TaxRate, bool, error) {
	if err := r.ready(); err != nil {
		return models.TaxRate{}, false, err
	}

	row, err := r.queries(ctx).ReviseTaxRate(ctx, taxdb.ReviseTaxRateParams{
		Name: next.Name, RateBps: next.RateBps, UpdatedAt: fromTime(now),
		ID: id, ReadName: read.Name, ReadRateBps: read.RateBps,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return models.TaxRate{}, false, nil
	case err != nil:
		return models.TaxRate{}, false, wrapDB(err, "the tax rate could not be revised: %s", id)
	}

	rate, err := toTaxRate(row)

	return rate, err == nil, err
}
