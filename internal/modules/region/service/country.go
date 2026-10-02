package service

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// AddCountryToRegion adds a country to a region.
//
// If the country belongs to another region, errors.Conflict is returned: a
// country can belong to AT MOST one region. The rule is guarded on the database
// side by a row lock (see repository.AssignCountry), so it holds under two
// concurrent requests as well.
//
// If the country is already in the SAME region, the call succeeds and the
// existing record is returned; a repeated admin request does not produce an
// error.
func (s *Service) AddCountryToRegion(ctx context.Context, regionID, countryCode string) (models.Country, error) {
	if err := s.ready(); err != nil {
		return models.Country{}, err
	}
	if err := requireRegionID(regionID); err != nil {
		return models.Country{}, err
	}
	code, err := NormalizeCountryCode(countryCode)
	if err != nil {
		return models.Country{}, err
	}

	country, err := s.repo.AssignCountry(ctx, regionID, code, s.clock())
	if err != nil {
		return models.Country{}, err
	}

	s.log.DebugContext(ctx, "country added to region",
		slog.String("region_id", regionID),
		slog.String("country_code", code),
	)
	return country, nil
}

// RemoveCountryFromRegion removes a country from a region.
//
// If the country does not belong to that region, errors.NotFound is returned;
// the target of the removal request is the "country in the region" record, and
// that record does not exist.
func (s *Service) RemoveCountryFromRegion(ctx context.Context, regionID, countryCode string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireRegionID(regionID); err != nil {
		return err
	}
	code, err := NormalizeCountryCode(countryCode)
	if err != nil {
		return err
	}

	if err := s.repo.UnassignCountry(ctx, regionID, code, s.clock()); err != nil {
		return err
	}

	s.log.DebugContext(ctx, "country removed from region",
		slog.String("region_id", regionID),
		slog.String("country_code", code),
	)
	return nil
}

// ListCountriesInput is the input for listing countries.
type ListCountriesInput struct {
	// RegionID, when set, limits the result to that region's countries; when
	// nil, all countries are returned.
	RegionID *string
	// Limit is the page size; if 0 is given, [DefaultLimit] is applied.
	Limit int32
	// Offset is the number of records to skip.
	Offset int32
}

// ListCountries returns the paginated country list.
//
// The country list is REFERENCE DATA and is loaded by the seed; there is only
// reading here. If a region filter is given, its id is validated first: without
// that validation an id of the wrong type would return an empty list and the
// client would conclude that the region has no countries.
func (s *Service) ListCountries(ctx context.Context, in ListCountriesInput) (Page[models.Country], error) {
	if err := s.ready(); err != nil {
		return Page[models.Country]{}, err
	}
	if in.RegionID != nil {
		if err := requireRegionID(*in.RegionID); err != nil {
			return Page[models.Country]{}, err
		}
	}
	limit, offset, err := normalizePaging(in.Limit, in.Offset)
	if err != nil {
		return Page[models.Country]{}, err
	}

	countries, total, err := s.repo.ListCountries(ctx, in.RegionID, limit, offset)
	if err != nil {
		return Page[models.Country]{}, err
	}
	return Page[models.Country]{Items: countries, Count: total, Limit: limit, Offset: offset}, nil
}

// ResolveRegionForCountry resolves the region from a country code.
//
// This is the path taken when a cart is created: the cart's currency and tax
// region are found from the customer's country. The happy path is a SINGLE
// query.
//
// When nothing is found there are three distinct cases. All three return
// errors.NotFound, but with different CODES; the caller knows from the code
// which fix is needed:
//
//   - The country is undefined (repository.CodeCountryNotFound) — the client
//     did not send a valid ISO code.
//   - The country is attached to no region ([CodeCountryUnassigned]) — the
//     operator has not opened sales to that country.
//   - The country is attached but its region is missing
//     ([CodeCountryRegionMissing]) — a data inconsistency; it does not arise
//     normally, because a region's countries are released when the region is
//     deleted.
//
// The distinction is made ONLY on the error path, with a second query; the
// happy path stays a single query.
func (s *Service) ResolveRegionForCountry(ctx context.Context, countryCode string) (models.Region, error) {
	if err := s.ready(); err != nil {
		return models.Region{}, err
	}
	code, err := NormalizeCountryCode(countryCode)
	if err != nil {
		return models.Region{}, err
	}

	region, err := s.repo.GetRegionByCountry(ctx, code)
	if err == nil {
		return region, nil
	}
	if !errors.IsNotFound(err) {
		return models.Region{}, err
	}

	country, lookupErr := s.repo.GetCountry(ctx, code)
	if lookupErr != nil {
		// If the country itself does not exist either, it is THIS error and not
		// the first one that carries the meaning: "country not found" is the
		// only information the client can act on.
		return models.Region{}, lookupErr
	}
	if country.RegionID != nil {
		return models.Region{}, errors.NotFound(CodeCountryRegionMissing,
			"country %s is attached to region %s but the region was not found", code, *country.RegionID)
	}
	return models.Region{}, errors.NotFound(CodeCountryUnassigned,
		"country %s is not attached to any region", code)
}
