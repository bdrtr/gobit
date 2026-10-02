package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// StoreRegion is the region view returned to the storefront: the region, its
// currency and its countries.
//
// The three travel TOGETHER because the storefront needs all three at once: the
// customer picks the region by their country and sees amounts with the
// currency's symbol and decimal digits. Had they been separate endpoints, a
// single region selection screen would make three requests.
//
// The tax rate and the automatic tax flag are DELIBERATELY absent: both are
// business configuration and do not go to the customer; tax appears already
// computed inside the cart total.
type StoreRegion struct {
	// Region is the region itself.
	Region models.Region
	// Currency is the region's currency; nil if it is not found in the
	// reference table.
	//
	// Returning nil is deliberate: putting a zero value in place of a missing
	// currency would report the decimal digits as 0 and show amounts at the
	// wrong scale. Because of the foreign key, this case cannot normally arise.
	Currency *models.Currency
	// Countries are the countries attached to the region; an empty slice if
	// there are none.
	Countries []models.Country
}

// ListStoreRegions returns the paginated region list for the storefront,
// together with each region's currency and countries.
//
// The number of queries is INDEPENDENT of the number of regions: regions,
// currencies and countries are fetched with three batch reads. Reading per
// region would mean N+1, and the storefront list is exactly where the most
// records come back.
func (s *Service) ListStoreRegions(ctx context.Context, limit, offset int32) (Page[StoreRegion], error) {
	if err := s.ready(); err != nil {
		return Page[StoreRegion]{}, err
	}
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return Page[StoreRegion]{}, err
	}

	regions, total, err := s.repo.ListRegions(ctx, limit, offset)
	if err != nil {
		return Page[StoreRegion]{}, err
	}

	items, err := s.decorate(ctx, regions)
	if err != nil {
		return Page[StoreRegion]{}, err
	}
	return Page[StoreRegion]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// GetStoreRegion returns a single region for the storefront, together with its
// currency and countries.
func (s *Service) GetStoreRegion(ctx context.Context, id string) (StoreRegion, error) {
	region, err := s.GetRegion(ctx, id)
	if err != nil {
		return StoreRegion{}, err
	}

	items, err := s.decorate(ctx, []models.Region{region})
	if err != nil {
		return StoreRegion{}, err
	}
	if len(items) == 0 {
		// decorate produces exactly one output per input; reaching this point
		// is impossible, but returning a zero value would be a silent error.
		return StoreRegion{}, errors.Internal(CodeDecorateFailed,
			"the region could not be converted to the storefront view: %s", id)
	}
	return items[0], nil
}

// decorate enriches the regions with their currencies and countries in BATCH.
//
// It makes two batch reads (currencies, countries) and PRESERVES the input
// order.
func (s *Service) decorate(ctx context.Context, regions []models.Region) ([]StoreRegion, error) {
	items := make([]StoreRegion, 0, len(regions))
	if len(regions) == 0 {
		return items, nil
	}

	codes := make([]string, 0, len(regions))
	ids := make([]string, 0, len(regions))
	for _, region := range regions {
		if !slices.Contains(codes, region.CurrencyCode) {
			codes = append(codes, region.CurrencyCode)
		}
		ids = append(ids, region.ID)
	}

	fetched, err := s.repo.GetCurrenciesByCodes(ctx, codes)
	if err != nil {
		return nil, err
	}
	currencies := make(map[string]models.Currency, len(fetched))
	for _, currency := range fetched {
		currencies[currency.Code] = currency
	}

	countries, err := s.repo.ListCountriesByRegions(ctx, ids)
	if err != nil {
		return nil, err
	}

	for _, region := range regions {
		item := StoreRegion{Region: region, Countries: countries[region.ID]}
		if item.Countries == nil {
			item.Countries = []models.Country{}
		}
		if currency, ok := currencies[region.CurrencyCode]; ok {
			item.Currency = &currency
		}
		items = append(items, item)
	}
	return items, nil
}
