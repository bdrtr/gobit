package api_test

import (
	"context"
	"slices"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
	"github.com/bdrtr/gobit/internal/modules/region/service"
)

// memRepo [service.Repository]'nin bellek içi uygulamasıdır.
//
// HTTP katmanı testleri GERÇEK servisi kullanır; yalnızca depo taklit edilir.
// Böylece doğrulama, hata sınıflandırması ve zarf biçimi uçtan uca sınanır ve
// handler'ların status kodu seçmediği (core/http'nin seçtiği) kanıtlanabilir.
type memRepo struct {
	regions    map[string]models.Region
	countries  map[string]models.Country
	currencies map[string]models.Currency
}

var _ service.Repository = (*memRepo)(nil)

// newMemRepo tohumlanmış bir bellek içi depo üretir.
func newMemRepo() *memRepo {
	m := &memRepo{
		regions:    map[string]models.Region{},
		countries:  map[string]models.Country{},
		currencies: map[string]models.Currency{},
	}
	for _, c := range []models.Currency{
		{Code: "TRY", Symbol: "₺", Name: "Turkish Lira", DecimalDigits: 2},
		{Code: "USD", Symbol: "$", Name: "US Dollar", DecimalDigits: 2},
		{Code: "JPY", Symbol: "¥", Name: "Yen", DecimalDigits: 0},
	} {
		m.currencies[c.Code] = c
	}
	for _, c := range []models.Country{
		{Code: "TR", Name: "Türkiye"},
		{Code: "DE", Name: "Germany"},
		{Code: "JP", Name: "Japan"},
	} {
		m.countries[c.Code] = c
	}
	return m
}

func (m *memRepo) CreateRegion(_ context.Context, region models.Region, now time.Time) (models.Region, error) {
	if _, ok := m.currencies[region.CurrencyCode]; !ok {
		return models.Region{}, errors.Invalid("region_unknown_currency",
			"bölge oluşturulamadı: para birimi tanımlı değil")
	}
	region.CreatedAt = now
	region.UpdatedAt = now
	m.regions[region.ID] = region
	return region, nil
}

func (m *memRepo) GetRegion(_ context.Context, id string) (models.Region, error) {
	region, ok := m.regions[id]
	if !ok || region.DeletedAt != nil {
		return models.Region{}, errors.NotFound("region_not_found", "bölge bulunamadı: %s", id)
	}
	return region, nil
}

func (m *memRepo) ListRegions(_ context.Context, limit, offset int32) ([]models.Region, int64, error) {
	live := m.liveRegions()
	total := int64(len(live))
	if int(offset) >= len(live) {
		return []models.Region{}, total, nil
	}
	end := min(int(offset)+int(limit), len(live))
	return live[offset:end], total, nil
}

// liveRegions silinmemiş bölgeleri kimliğe göre sıralı döner.
func (m *memRepo) liveRegions() []models.Region {
	out := make([]models.Region, 0, len(m.regions))
	for _, region := range m.regions {
		if region.DeletedAt == nil {
			out = append(out, region)
		}
	}
	slices.SortFunc(out, func(a, b models.Region) int {
		return compareStrings(a.ID, b.ID)
	})
	return out
}

func (m *memRepo) GetRegionsByIDs(_ context.Context, ids []string) ([]models.Region, error) {
	out := make([]models.Region, 0, len(ids))
	for _, region := range m.liveRegions() {
		if slices.Contains(ids, region.ID) {
			out = append(out, region)
		}
	}
	return out, nil
}

func (m *memRepo) UpdateRegion(
	ctx context.Context,
	id string,
	patch models.RegionPatch,
	now time.Time,
) (models.Region, error) {
	current, err := m.GetRegion(ctx, id)
	if err != nil {
		return models.Region{}, err
	}
	next := current.Patched(patch)
	if _, ok := m.currencies[next.CurrencyCode]; !ok {
		return models.Region{}, errors.Invalid("region_unknown_currency",
			"bölge güncellenemedi: para birimi tanımlı değil")
	}
	next.UpdatedAt = now
	m.regions[id] = next
	return next, nil
}

func (m *memRepo) DeleteRegion(ctx context.Context, id string, now time.Time) error {
	current, err := m.GetRegion(ctx, id)
	if err != nil {
		return err
	}
	deleted := now
	current.DeletedAt = &deleted
	m.regions[id] = current

	for code, country := range m.countries {
		if country.RegionID != nil && *country.RegionID == id {
			country.RegionID = nil
			m.countries[code] = country
		}
	}
	return nil
}

func (m *memRepo) GetRegionByCountry(ctx context.Context, countryCode string) (models.Region, error) {
	country, ok := m.countries[countryCode]
	if !ok || country.RegionID == nil {
		return models.Region{}, errors.NotFound("region_not_found",
			"%s ülkesi için bölge bulunamadı", countryCode)
	}
	return m.GetRegion(ctx, *country.RegionID)
}

func (m *memRepo) AssignCountry(
	ctx context.Context,
	regionID, countryCode string,
	now time.Time,
) (models.Country, error) {
	if _, err := m.GetRegion(ctx, regionID); err != nil {
		return models.Country{}, err
	}
	country, ok := m.countries[countryCode]
	if !ok {
		return models.Country{}, errors.NotFound("country_not_found", "ülke bulunamadı: %s", countryCode)
	}
	if country.RegionID != nil {
		if *country.RegionID == regionID {
			return country, nil
		}
		return models.Country{}, errors.Conflict("country_already_in_region",
			"%s ülkesi zaten %s bölgesine ait", countryCode, *country.RegionID)
	}

	assigned := regionID
	country.RegionID = &assigned
	country.UpdatedAt = now
	m.countries[countryCode] = country
	return country, nil
}

func (m *memRepo) UnassignCountry(_ context.Context, regionID, countryCode string, now time.Time) error {
	country, ok := m.countries[countryCode]
	if !ok {
		return errors.NotFound("country_not_found", "ülke bulunamadı: %s", countryCode)
	}
	if country.RegionID == nil || *country.RegionID != regionID {
		return errors.NotFound("country_not_in_region",
			"%s ülkesi %s bölgesine ait değil", countryCode, regionID)
	}
	country.RegionID = nil
	country.UpdatedAt = now
	m.countries[countryCode] = country
	return nil
}

func (m *memRepo) GetCountry(_ context.Context, code string) (models.Country, error) {
	country, ok := m.countries[code]
	if !ok {
		return models.Country{}, errors.NotFound("country_not_found", "ülke bulunamadı: %s", code)
	}
	return country, nil
}

func (m *memRepo) ListCountries(
	_ context.Context,
	regionID *string,
	limit, offset int32,
) ([]models.Country, int64, error) {
	matched := make([]models.Country, 0, len(m.countries))
	for _, country := range m.countries {
		if regionID != nil && (country.RegionID == nil || *country.RegionID != *regionID) {
			continue
		}
		matched = append(matched, country)
	}
	slices.SortFunc(matched, func(a, b models.Country) int {
		return compareStrings(a.Code, b.Code)
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
	byRegion := map[string][]models.Country{}
	for _, country := range m.countries {
		if country.RegionID == nil || !slices.Contains(regionIDs, *country.RegionID) {
			continue
		}
		byRegion[*country.RegionID] = append(byRegion[*country.RegionID], country)
	}
	for id := range byRegion {
		slices.SortFunc(byRegion[id], func(a, b models.Country) int {
			return compareStrings(a.Code, b.Code)
		})
	}
	return byRegion, nil
}

func (m *memRepo) GetCurrency(_ context.Context, code string) (models.Currency, error) {
	currency, ok := m.currencies[code]
	if !ok {
		return models.Currency{}, errors.NotFound("currency_not_found", "para birimi bulunamadı: %s", code)
	}
	return currency, nil
}

func (m *memRepo) ListCurrencies(_ context.Context, limit, offset int32) ([]models.Currency, int64, error) {
	all := make([]models.Currency, 0, len(m.currencies))
	for _, currency := range m.currencies {
		all = append(all, currency)
	}
	slices.SortFunc(all, func(a, b models.Currency) int {
		return compareStrings(a.Code, b.Code)
	})

	total := int64(len(all))
	if int(offset) >= len(all) {
		return []models.Currency{}, total, nil
	}
	end := min(int(offset)+int(limit), len(all))
	return all[offset:end], total, nil
}

func (m *memRepo) GetCurrenciesByCodes(_ context.Context, codes []string) ([]models.Currency, error) {
	out := make([]models.Currency, 0, len(codes))
	for _, code := range codes {
		if currency, ok := m.currencies[code]; ok {
			out = append(out, currency)
		}
	}
	return out, nil
}

// compareStrings iki dizeyi sözlüksel olarak karşılaştırır.
func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
