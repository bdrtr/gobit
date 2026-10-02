package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/modules/region/models"
	"github.com/bdrtr/gobit/internal/modules/region/repository/regiondb"
)

// ReviseRegion writes the region's terms only while they are the ones read,
// and reports whether it wrote (ADR 0362).
func (r *Repo) ReviseRegion(
	ctx context.Context, id string, read, next models.RegionTerms, now time.Time,
) (models.Region, bool, error) {
	row, err := r.q.ReviseRegion(ctx, regiondb.ReviseRegionParams{
		Name: next.Name, AutomaticTaxes: next.AutomaticTaxes, TaxRate: next.TaxRate, UpdatedAt: fromTime(now),
		ID: id, ReadName: read.Name, ReadAutomaticTaxes: read.AutomaticTaxes, ReadTaxRate: read.TaxRate,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return models.Region{}, false, nil
	case err != nil:
		return models.Region{}, false, wrapDB(err, "the region could not be revised: %s", id)
	}

	return toRegion(row), true, nil
}
