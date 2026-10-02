package service

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// memRepo is the in-memory implementation of [Repository].
//
// Its purpose is to verify the service's RULES without a database: id
// generation, normalization, partial update, conflict classification and the
// number of batch reads. Database-specific claims (the row lock separating two
// concurrent assignments, soft delete being filtered on the SQL side, the
// foreign key rejecting an undefined currency) are proven NOT HERE but in the
// integration tests — a fake repository cannot verify a rule it wrote itself.
//
// Repository calls are counted (calls): that is the evidence for the claim "no
// query is made per record".
type memRepo struct {
	mu sync.Mutex

	regions    map[string]models.Region
	countries  map[string]models.Country
	currencies map[string]models.Currency

	// calls is the call counter keyed by method name.
	calls map[string]int
	// failOn holds, for each method name set in it, the error that call
	// returns.
	failOn map[string]error
	// lastListLimit and lastListOffset are the paging values APPLIED to the
	// last ListRegions call; they prove that the service really applies the
	// bounds.
	lastListLimit  int32
	lastListOffset int32
}

var _ Repository = (*memRepo)(nil)

// newMemRepo produces a seeded in-memory repository.
//
// The currencies cover all three classes of the real seed: 2 decimal digits
// (TRY, USD), 0 decimal digits (JPY) and 3 decimal digits (KWD). Code that
// assumes a fixed factor of 100 gives a wrong result in two of these three
// classes.
func newMemRepo() *memRepo {
	m := &memRepo{
		regions:    map[string]models.Region{},
		countries:  map[string]models.Country{},
		currencies: map[string]models.Currency{},
		calls:      map[string]int{},
		failOn:     map[string]error{},
	}
	for _, c := range []models.Currency{
		{Code: "TRY", Symbol: "₺", Name: "Turkish Lira", DecimalDigits: 2},
		{Code: "USD", Symbol: "$", Name: "US Dollar", DecimalDigits: 2},
		{Code: "JPY", Symbol: "¥", Name: "Yen", DecimalDigits: 0},
		{Code: "KWD", Symbol: "د.ك", Name: "Kuwaiti Dinar", DecimalDigits: 3},
	} {
		m.currencies[c.Code] = c
	}
	for _, c := range []models.Country{
		{Code: "TR", Name: "T\u00fcrkiye"},
		{Code: "DE", Name: "Germany"},
		{Code: "US", Name: "United States of America"},
		{Code: "JP", Name: "Japan"},
	} {
		m.countries[c.Code] = c
	}
	return m
}

// track counts the call and returns the injected error, if there is one.
// The caller must hold m.mu.
func (m *memRepo) track(name string) error {
	m.calls[name]++
	return m.failOn[name]
}

// lastPaging returns the limit and offset applied to the last ListRegions call.
func (m *memRepo) lastPaging() (limit, offset int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastListLimit, m.lastListOffset
}

// callCount returns how many times the given method was called.
func (m *memRepo) callCount(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[name]
}

// resetCalls zeroes the call counters; it keeps the setup calls out of the
// assertion.
func (m *memRepo) resetCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = map[string]int{}
}

func (m *memRepo) CreateRegion(_ context.Context, region models.Region, now time.Time) (models.Region, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CreateRegion"); err != nil {
		return models.Region{}, err
	}

	// In the real repository this check is the foreign key; the fake
	// repository keeps the same promise with the same error class.
	if _, ok := m.currencies[region.CurrencyCode]; !ok {
		return models.Region{}, errors.Invalid("region_unknown_currency",
			"the region could not be created: the currency is not defined")
	}

	region.CreatedAt = now
	region.UpdatedAt = now
	m.regions[region.ID] = region
	return region, nil
}

func (m *memRepo) GetRegion(_ context.Context, id string) (models.Region, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("GetRegion"); err != nil {
		return models.Region{}, err
	}
	return m.getRegionLocked(id)
}

// getRegionLocked reads the region under the lock. The caller must hold m.mu.
func (m *memRepo) getRegionLocked(id string) (models.Region, error) {
	region, ok := m.regions[id]
	if !ok || region.DeletedAt != nil {
		return models.Region{}, errors.NotFound("region_not_found", "region not found: %s", id)
	}
	return region, nil
}

func (m *memRepo) ListRegions(_ context.Context, limit, offset int32) ([]models.Region, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListRegions"); err != nil {
		return nil, 0, err
	}
	m.lastListLimit, m.lastListOffset = limit, offset

	live := m.liveRegionsLocked()
	total := int64(len(live))
	if int(offset) >= len(live) {
		return []models.Region{}, total, nil
	}
	end := min(int(offset)+int(limit), len(live))
	return slices.Clone(live[offset:end]), total, nil
}

// liveRegionsLocked returns the regions that are not deleted, sorted by id.
// The caller must hold m.mu.
func (m *memRepo) liveRegionsLocked() []models.Region {
	out := make([]models.Region, 0, len(m.regions))
	for _, region := range m.regions {
		if region.DeletedAt == nil {
			out = append(out, region)
		}
	}
	slices.SortFunc(out, func(a, b models.Region) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return out
}

func (m *memRepo) GetRegionsByIDs(_ context.Context, ids []string) ([]models.Region, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("GetRegionsByIDs"); err != nil {
		return nil, err
	}

	out := make([]models.Region, 0, len(ids))
	for _, region := range m.liveRegionsLocked() {
		if slices.Contains(ids, region.ID) {
			out = append(out, region)
		}
	}
	return out, nil
}

// ReviseRegion mirrors the query: the terms are written only on a live region
// whose terms are the ones read.
func (m *memRepo) ReviseRegion(
	_ context.Context, id string, read, next models.RegionTerms, now time.Time,
) (models.Region, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ReviseRegion"); err != nil {
		return models.Region{}, false, err
	}

	// As in the UPDATE, a region gone or revised since matches no row.
	current, ok := m.regions[id]
	if !ok || current.DeletedAt != nil || current.Terms() != read {
		return models.Region{}, false, nil
	}
	current.Name, current.AutomaticTaxes, current.TaxRate, current.UpdatedAt =
		next.Name, next.AutomaticTaxes, next.TaxRate, now
	m.regions[id] = current

	return current, true, nil
}

func (m *memRepo) UpdateRegion(
	_ context.Context,
	id string,
	patch models.RegionPatch,
	now time.Time,
) (models.Region, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("UpdateRegion"); err != nil {
		return models.Region{}, err
	}

	current, err := m.getRegionLocked(id)
	if err != nil {
		return models.Region{}, err
	}

	next := current.Patched(patch)
	if _, ok := m.currencies[next.CurrencyCode]; !ok {
		return models.Region{}, errors.Invalid("region_unknown_currency",
			"the region could not be updated: the currency is not defined")
	}
	next.UpdatedAt = now
	m.regions[id] = next
	return next, nil
}

func (m *memRepo) DeleteRegion(_ context.Context, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("DeleteRegion"); err != nil {
		return err
	}

	current, err := m.getRegionLocked(id)
	if err != nil {
		return err
	}
	deleted := now
	current.DeletedAt = &deleted
	current.UpdatedAt = now
	m.regions[id] = current

	// The real repository deletes the region and releases its countries in a
	// SINGLE transaction.
	for code, country := range m.countries {
		if country.RegionID != nil && *country.RegionID == id {
			country.RegionID = nil
			country.UpdatedAt = now
			m.countries[code] = country
		}
	}
	return nil
}

func (m *memRepo) GetRegionByCountry(_ context.Context, countryCode string) (models.Region, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("GetRegionByCountry"); err != nil {
		return models.Region{}, err
	}

	country, ok := m.countries[countryCode]
	if !ok || country.RegionID == nil {
		return models.Region{}, errors.NotFound("region_not_found",
			"no region found for country %s", countryCode)
	}
	return m.getRegionLocked(*country.RegionID)
}

func (m *memRepo) AssignCountry(
	_ context.Context,
	regionID, countryCode string,
	now time.Time,
) (models.Country, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("AssignCountry"); err != nil {
		return models.Country{}, err
	}

	if _, err := m.getRegionLocked(regionID); err != nil {
		return models.Country{}, err
	}
	country, ok := m.countries[countryCode]
	if !ok {
		return models.Country{}, errors.NotFound("country_not_found", "country not found: %s", countryCode)
	}

	if country.RegionID != nil {
		if *country.RegionID == regionID {
			return country, nil
		}
		return models.Country{}, errors.Conflict("country_already_in_region",
			"country %s already belongs to region %s", countryCode, *country.RegionID)
	}

	assigned := regionID
	country.RegionID = &assigned
	country.UpdatedAt = now
	m.countries[countryCode] = country
	return country, nil
}

func (m *memRepo) UnassignCountry(_ context.Context, regionID, countryCode string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("UnassignCountry"); err != nil {
		return err
	}

	country, ok := m.countries[countryCode]
	if !ok {
		return errors.NotFound("country_not_found", "country not found: %s", countryCode)
	}
	if country.RegionID == nil || *country.RegionID != regionID {
		return errors.NotFound("country_not_in_region",
			"country %s does not belong to region %s", countryCode, regionID)
	}

	country.RegionID = nil
	country.UpdatedAt = now
	m.countries[countryCode] = country
	return nil
}

func (m *memRepo) GetCountry(_ context.Context, code string) (models.Country, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("GetCountry"); err != nil {
		return models.Country{}, err
	}

	country, ok := m.countries[code]
	if !ok {
		return models.Country{}, errors.NotFound("country_not_found", "country not found: %s", code)
	}
	return country, nil
}

func (m *memRepo) ListCountries(
	_ context.Context,
	regionID *string,
	limit, offset int32,
) ([]models.Country, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListCountries"); err != nil {
		return nil, 0, err
	}

	matched := make([]models.Country, 0, len(m.countries))
	for _, country := range m.countries {
		if regionID != nil && (country.RegionID == nil || *country.RegionID != *regionID) {
			continue
		}
		matched = append(matched, country)
	}
	slices.SortFunc(matched, func(a, b models.Country) int {
		switch {
		case a.Code < b.Code:
			return -1
		case a.Code > b.Code:
			return 1
		default:
			return 0
		}
	})

	total := int64(len(matched))
	if int(offset) >= len(matched) {
		return []models.Country{}, total, nil
	}
	end := min(int(offset)+int(limit), len(matched))
	return matched[offset:end], total, nil
}

func (m *memRepo) ListCountriesByRegions(
	_ context.Context,
	regionIDs []string,
) (map[string][]models.Country, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListCountriesByRegions"); err != nil {
		return nil, err
	}

	byRegion := map[string][]models.Country{}
	for _, country := range m.countries {
		if country.RegionID == nil {
			continue
		}
		if !slices.Contains(regionIDs, *country.RegionID) {
			continue
		}
		byRegion[*country.RegionID] = append(byRegion[*country.RegionID], country)
	}
	for id := range byRegion {
		slices.SortFunc(byRegion[id], func(a, b models.Country) int {
			switch {
			case a.Code < b.Code:
				return -1
			case a.Code > b.Code:
				return 1
			default:
				return 0
			}
		})
	}
	return byRegion, nil
}

func (m *memRepo) GetCurrency(_ context.Context, code string) (models.Currency, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("GetCurrency"); err != nil {
		return models.Currency{}, err
	}

	currency, ok := m.currencies[code]
	if !ok {
		return models.Currency{}, errors.NotFound("currency_not_found", "currency not found: %s", code)
	}
	return currency, nil
}

func (m *memRepo) ListCurrencies(_ context.Context, limit, offset int32) ([]models.Currency, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListCurrencies"); err != nil {
		return nil, 0, err
	}

	all := make([]models.Currency, 0, len(m.currencies))
	for _, currency := range m.currencies {
		all = append(all, currency)
	}
	slices.SortFunc(all, func(a, b models.Currency) int {
		switch {
		case a.Code < b.Code:
			return -1
		case a.Code > b.Code:
			return 1
		default:
			return 0
		}
	})

	total := int64(len(all))
	if int(offset) >= len(all) {
		return []models.Currency{}, total, nil
	}
	end := min(int(offset)+int(limit), len(all))
	return all[offset:end], total, nil
}

func (m *memRepo) GetCurrenciesByCodes(_ context.Context, codes []string) ([]models.Currency, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("GetCurrenciesByCodes"); err != nil {
		return nil, err
	}

	out := make([]models.Currency, 0, len(codes))
	for _, code := range codes {
		if currency, ok := m.currencies[code]; ok {
			out = append(out, currency)
		}
	}
	return out, nil
}
