package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/region/models"
	"github.com/bdrtr/gobit/internal/modules/region/repository/regiondb"
)

// CreateRegion writes a new region and returns the written row.
//
// If an undefined currency is given, the foreign key violation is converted
// into errors.Invalid (see wrapDB): checking first with a SELECT whether it
// exists would be open to a race — the currency could be deleted between the
// check and the write.
func (r *Repo) CreateRegion(ctx context.Context, region models.Region, now time.Time) (models.Region, error) {
	if err := r.ready(); err != nil {
		return models.Region{}, err
	}

	row, err := r.q.InsertRegion(ctx, regiondb.InsertRegionParams{
		ID:             region.ID,
		Name:           region.Name,
		CurrencyCode:   region.CurrencyCode,
		AutomaticTaxes: region.AutomaticTaxes,
		TaxRate:        region.TaxRate,
		CreatedAt:      fromTime(now),
	})
	if err != nil {
		return models.Region{}, wrapDB(err, "the region could not be created")
	}
	return toRegion(row), nil
}

// GetRegion returns the region by its id; errors.NotFound if there is none.
func (r *Repo) GetRegion(ctx context.Context, id string) (models.Region, error) {
	if err := r.ready(); err != nil {
		return models.Region{}, err
	}

	row, err := r.q.GetRegion(ctx, id)
	if err != nil {
		return models.Region{}, notFoundOr(err, CodeRegionNotFound, "region not found: %s", id)
	}
	return toRegion(row), nil
}

// ListRegions returns a paginated list of regions and the TOTAL record count.
//
// The total is independent of the page length; the "count" field in the API
// envelope is what lets the client know how many pages there are.
func (r *Repo) ListRegions(ctx context.Context, limit, offset int32) ([]models.Region, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListRegions(ctx, regiondb.ListRegionsParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, wrapDB(err, "the region list could not be read")
	}

	total, err := r.q.CountRegions(ctx)
	if err != nil {
		return nil, 0, wrapDB(err, "the region count could not be read")
	}

	regions := make([]models.Region, 0, len(rows))
	for i := range rows {
		regions = append(regions, toRegion(rows[i]))
	}
	return regions, total, nil
}

// GetRegionsByIDs returns the regions matching the given ids in a SINGLE
// query. No record is returned for an id that is not found; that is not an
// error (the Query layer's FetchByIDs contract, ADR 0004).
func (r *Repo) GetRegionsByIDs(ctx context.Context, ids []string) ([]models.Region, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.Region{}, nil
	}

	rows, err := r.q.GetRegionsByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "the regions could not be read")
	}

	regions := make([]models.Region, 0, len(rows))
	for i := range rows {
		regions = append(regions, toRegion(rows[i]))
	}
	return regions, nil
}

// UpdateRegion applies the patch on top of the row read UNDER A LOCK.
//
// The read-modify-write cycle runs in a single transaction under a row lock:
// of two concurrent partial updates without a lock, the second would write the
// field the first had written back with its old value (lost update). The patch
// itself is a pure transformation ([models.Region.Patched]) and can be tested
// without a database.
func (r *Repo) UpdateRegion(
	ctx context.Context,
	id string,
	patch models.RegionPatch,
	now time.Time,
) (models.Region, error) {
	var updated models.Region

	err := r.inTx(ctx, func(q *regiondb.Queries) error {
		current, err := q.GetRegionForUpdate(ctx, id)
		if err != nil {
			return notFoundOr(err, CodeRegionNotFound, "region not found: %s", id)
		}

		next := toRegion(current).Patched(patch)
		row, err := q.UpdateRegion(ctx, regiondb.UpdateRegionParams{
			ID:             id,
			Name:           next.Name,
			CurrencyCode:   next.CurrencyCode,
			AutomaticTaxes: next.AutomaticTaxes,
			TaxRate:        next.TaxRate,
			UpdatedAt:      fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "the region could not be updated: %s", id)
		}
		updated = toRegion(row)
		return nil
	})
	if err != nil {
		return models.Region{}, err
	}
	return updated, nil
}

// DeleteRegion deletes the region with a soft delete and RELEASES its
// countries.
//
// The two steps are in a single transaction and their order is the lock order:
// first the region, then the countries. Had the countries not been released
// they would stay bound to a dead region, could not be added to any other
// region, and ResolveRegionForCountry would permanently return "not found" for
// them.
func (r *Repo) DeleteRegion(ctx context.Context, id string, now time.Time) error {
	return r.inTx(ctx, func(q *regiondb.Queries) error {
		if _, err := q.SoftDeleteRegion(ctx, regiondb.SoftDeleteRegionParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodeRegionNotFound, "region not found: %s", id)
		}

		if err := q.ClearRegionCountries(ctx, regiondb.ClearRegionCountriesParams{
			RegionID:  id,
			UpdatedAt: fromTime(now),
		}); err != nil {
			return wrapDB(err, "the countries of the region could not be released: %s", id)
		}
		return nil
	})
}

// GetRegionByCountry returns the region matching the country code in a SINGLE
// query.
//
// If the country is undefined, is bound to no region, or the region it is bound
// to has been deleted, it returns errors.NotFound; telling the three cases
// apart is the service's job and is done ONLY on the error path (see
// service.ResolveRegionForCountry).
func (r *Repo) GetRegionByCountry(ctx context.Context, countryCode string) (models.Region, error) {
	if err := r.ready(); err != nil {
		return models.Region{}, err
	}

	row, err := r.q.GetRegionByCountry(ctx, countryCode)
	if err != nil {
		return models.Region{}, notFoundOr(err, CodeRegionNotFound,
			"no region found for country %s", countryCode)
	}
	return toRegion(row), nil
}
