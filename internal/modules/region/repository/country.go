package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
	"github.com/bdrtr/gobit/internal/modules/region/repository/regiondb"
)

// AssignCountry binds the country to the region and returns the updated
// country.
//
// The rule "a country can belong to at most one region" is guarded HERE:
//
//   - The region row is locked SHARED first (the first step of the lock
//     order), so a country cannot be added to a region that is being deleted.
//   - The country row is then locked EXCLUSIVELY. Of two requests trying to add
//     the same country to two different regions, the second waits here; once
//     its wait is over it reads the CURRENT version of the row, sees that the
//     country has been taken and returns errors.Conflict. Without the lock both
//     would see an empty region_id and the second would silently overwrite
//     what the first wrote.
//
// The call is IDEMPOTENT: if the country is already in the same region, the
// existing record is returned without a write. A repeated admin request
// failing with a conflict error would make no sense — the requested state
// already holds.
func (r *Repo) AssignCountry(
	ctx context.Context,
	regionID, countryCode string,
	now time.Time,
) (models.Country, error) {
	var assigned models.Country

	err := r.inTx(ctx, func(q *regiondb.Queries) error {
		if _, err := q.GetRegionForShare(ctx, regionID); err != nil {
			return notFoundOr(err, CodeRegionNotFound, "region not found: %s", regionID)
		}

		current, err := q.GetCountryForUpdate(ctx, countryCode)
		if err != nil {
			return notFoundOr(err, CodeCountryNotFound, "country not found: %s", countryCode)
		}

		if current.RegionID != nil {
			if *current.RegionID == regionID {
				assigned = toCountry(current)
				return nil
			}
			return errors.Conflict(CodeCountryTaken,
				"country %s already belongs to region %s; a country can belong to at most one region",
				countryCode, *current.RegionID)
		}

		row, err := q.SetCountryRegion(ctx, regiondb.SetCountryRegionParams{
			Iso2:      countryCode,
			RegionID:  regionID,
			UpdatedAt: fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "the country could not be added to the region: %s", countryCode)
		}
		assigned = toCountry(row)
		return nil
	})
	if err != nil {
		return models.Country{}, err
	}
	return assigned, nil
}

// UnassignCountry detaches the country from the given region.
//
// If the country does not belong to that region it returns errors.NotFound:
// the target of the delete request is the "country in the region" record, and
// that record does not exist. Returning success silently would mean a call made
// with the wrong region id is taken for a successful one.
func (r *Repo) UnassignCountry(ctx context.Context, regionID, countryCode string, now time.Time) error {
	return r.inTx(ctx, func(q *regiondb.Queries) error {
		current, err := q.GetCountryForUpdate(ctx, countryCode)
		if err != nil {
			return notFoundOr(err, CodeCountryNotFound, "country not found: %s", countryCode)
		}
		if current.RegionID == nil || *current.RegionID != regionID {
			return errors.NotFound(CodeCountryNotInRegion,
				"country %s does not belong to region %s", countryCode, regionID)
		}

		if _, err := q.ClearCountryRegion(ctx, regiondb.ClearCountryRegionParams{
			Iso2:      countryCode,
			RegionID:  regionID,
			UpdatedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodeCountryNotInRegion,
				"country %s could not be removed from region %s", countryCode, regionID)
		}
		return nil
	})
}

// GetCountry returns the country by its code; errors.NotFound if there is none.
func (r *Repo) GetCountry(ctx context.Context, code string) (models.Country, error) {
	if err := r.ready(); err != nil {
		return models.Country{}, err
	}

	row, err := r.q.GetCountry(ctx, code)
	if err != nil {
		return models.Country{}, notFoundOr(err, CodeCountryNotFound, "country not found: %s", code)
	}
	return toCountry(row), nil
}

// ListCountries returns a paginated list of countries and the TOTAL record
// count.
//
// If regionID is nil no filter is applied; if it is set, only that region's
// countries are returned.
func (r *Repo) ListCountries(
	ctx context.Context,
	regionID *string,
	limit, offset int32,
) ([]models.Country, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListCountries(ctx, regiondb.ListCountriesParams{
		RegionID: regionID,
		Lim:      limit,
		Off:      offset,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "the country list could not be read")
	}

	total, err := r.q.CountCountries(ctx, regionID)
	if err != nil {
		return nil, 0, wrapDB(err, "the country count could not be read")
	}

	countries := make([]models.Country, 0, len(rows))
	for i := range rows {
		countries = append(countries, toCountry(rows[i]))
	}
	return countries, total, nil
}

// ListCountriesByRegions returns the countries of several regions in a SINGLE
// query, grouped by region id.
//
// The Query provider returns regions together with their countries; a separate
// query per region would mean N+1 (ADR 0004).
func (r *Repo) ListCountriesByRegions(
	ctx context.Context,
	regionIDs []string,
) (map[string][]models.Country, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(regionIDs) == 0 {
		return map[string][]models.Country{}, nil
	}

	rows, err := r.q.ListCountriesByRegions(ctx, regionIDs)
	if err != nil {
		return nil, wrapDB(err, "the countries of the regions could not be read")
	}

	byRegion := make(map[string][]models.Country, len(regionIDs))
	for i := range rows {
		regionID := rows[i].RegionID
		if regionID == nil {
			// The query filters on region_id = ANY(...), so a NULL row cannot
			// come back; if one did, writing it into a group without an id
			// would be a silent error.
			continue
		}
		byRegion[*regionID] = append(byRegion[*regionID], toCountry(rows[i]))
	}
	return byRegion, nil
}
