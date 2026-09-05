package service

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// memRepo [Repository]'nin bellek içi uygulamasıdır.
//
// Amacı, servisin KURALLARINI veritabanı olmadan doğrulayabilmektir: kimlik
// üretimi, normalleştirme, kısmi güncelleme, çakışma sınıflandırması ve toplu
// okuma sayısı. Veritabanına özgü iddialar (satır kilidinin eşzamanlı iki
// atamayı ayırması, soft delete'in SQL tarafında süzülmesi, foreign key'in
// tanımsız para birimini reddetmesi) BURADA DEĞİL, entegrasyon testlerinde
// kanıtlanır — sahte bir depo kendi yazdığı kuralı doğrulayamaz.
//
// Depo çağrıları sayılır (calls): "kayıt başına sorgu yapılmıyor" iddiasının
// kanıtı budur.
type memRepo struct {
	mu sync.Mutex

	regions    map[string]models.Region
	countries  map[string]models.Country
	currencies map[string]models.Currency

	// calls metot adına göre çağrı sayacıdır.
	calls map[string]int
	// failOn dolu bir metot adı için o çağrının döneceği hatadır.
	failOn map[string]error
	// lastListLimit ve lastListOffset son ListRegions çağrısına UYGULANAN
	// sayfalama değerleridir; servisin sınırları gerçekten uyguladığı bunlarla
	// kanıtlanır.
	lastListLimit  int32
	lastListOffset int32
}

var _ Repository = (*memRepo)(nil)

// newMemRepo tohumlanmış bir bellek içi depo üretir.
//
// Para birimleri gerçek tohumun üç sınıfını da kapsar: 2 basamaklı (TRY, USD),
// 0 basamaklı (JPY) ve 3 basamaklı (KWD). Sabit 100 çarpanı varsayan bir kod
// bu üç sınıfın ikisinde yanlış sonuç verir.
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
		{Code: "TR", Name: "Türkiye"},
		{Code: "DE", Name: "Germany"},
		{Code: "US", Name: "United States of America"},
		{Code: "JP", Name: "Japan"},
	} {
		m.countries[c.Code] = c
	}
	return m
}

// track çağrıyı sayar ve enjekte edilmiş hata varsa döner.
// Çağıran m.mu'yu tutmalıdır.
func (m *memRepo) track(name string) error {
	m.calls[name]++
	return m.failOn[name]
}

// lastPaging son ListRegions çağrısına uygulanan limit ve offset'i döner.
func (m *memRepo) lastPaging() (limit, offset int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastListLimit, m.lastListOffset
}

// callCount verilen metodun kaç kez çağrıldığını döner.
func (m *memRepo) callCount(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[name]
}

// resetCalls çağrı sayaçlarını sıfırlar; kurulum çağrılarının iddiaya
// karışmaması içindir.
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

	// Gerçek depoda bu denetim foreign key'dir; sahte depo aynı sözü aynı
	// hata sınıfıyla tutar.
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("GetRegion"); err != nil {
		return models.Region{}, err
	}
	return m.getRegionLocked(id)
}

// getRegionLocked kilit altında bölgeyi okur. Çağıran m.mu'yu tutmalıdır.
func (m *memRepo) getRegionLocked(id string) (models.Region, error) {
	region, ok := m.regions[id]
	if !ok || region.DeletedAt != nil {
		return models.Region{}, errors.NotFound("region_not_found", "bölge bulunamadı: %s", id)
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

// liveRegionsLocked silinmemiş bölgeleri kimliğe göre sıralı döner.
// Çağıran m.mu'yu tutmalıdır.
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
			"bölge güncellenemedi: para birimi tanımlı değil")
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

	// Gerçek depo silme ile ülkeleri serbest bırakmayı TEK işlemde yapar.
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
	if !ok || country.DeletedAt != nil || country.RegionID == nil {
		return models.Region{}, errors.NotFound("region_not_found",
			"%s ülkesi için bölge bulunamadı", countryCode)
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
	if !ok || country.DeletedAt != nil {
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("UnassignCountry"); err != nil {
		return err
	}

	country, ok := m.countries[countryCode]
	if !ok || country.DeletedAt != nil {
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("GetCountry"); err != nil {
		return models.Country{}, err
	}

	country, ok := m.countries[code]
	if !ok || country.DeletedAt != nil {
		return models.Country{}, errors.NotFound("country_not_found", "ülke bulunamadı: %s", code)
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
		if country.DeletedAt != nil {
			continue
		}
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
		if country.DeletedAt != nil || country.RegionID == nil {
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
	if !ok || currency.DeletedAt != nil {
		return models.Currency{}, errors.NotFound("currency_not_found", "para birimi bulunamadı: %s", code)
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
		if currency.DeletedAt == nil {
			all = append(all, currency)
		}
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
		if currency, ok := m.currencies[code]; ok && currency.DeletedAt == nil {
			out = append(out, currency)
		}
	}
	return out, nil
}
