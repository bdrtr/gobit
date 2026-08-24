package api_test

import (
	"context"
	"slices"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// memRepo [service.Repository]'nin bellek içi uygulamasıdır.
//
// HTTP katmanı testleri GERÇEK servisi kullanır; yalnızca depo taklit edilir.
// Böylece doğrulama, hata sınıflandırması ve zarf biçimi uçtan uca sınanır ve
// handler'ların status kodu SEÇMEDİĞİ (core/http'nin seçtiği) kanıtlanabilir.
type memRepo struct {
	regions map[string]models.TaxRegion
	rates   map[string]models.TaxRate
	rules   map[string]models.TaxRateRule
}

var _ service.Repository = (*memRepo)(nil)

// newMemRepo boş bir bellek içi depo üretir.
func newMemRepo() *memRepo {
	return &memRepo{
		regions: map[string]models.TaxRegion{},
		rates:   map[string]models.TaxRate{},
		rules:   map[string]models.TaxRateRule{},
	}
}

// CreateTaxRegion bölgeyi yazar; ülkenin ikinci kökünü reddeder.
func (m *memRepo) CreateTaxRegion(_ context.Context, region models.TaxRegion, now time.Time) (models.TaxRegion, error) {
	// Anahtar üzerinden dolaşılır: değerle dolaşmak her turda modelin
	// tamamını kopyalar.
	for key := range m.regions {
		existing := m.regions[key]
		if existing.DeletedAt == nil && existing.CountryCode == region.CountryCode &&
			existing.IsRoot() && region.IsRoot() {
			return models.TaxRegion{}, errors.Conflict("tax_duplicate", "kök bölge zaten var")
		}
	}
	region.CreatedAt = now.UTC()
	region.UpdatedAt = now.UTC()
	m.regions[region.ID] = region
	return region, nil
}

// GetTaxRegion kimliğe göre canlı bölgeyi döner.
func (m *memRepo) GetTaxRegion(_ context.Context, id string) (models.TaxRegion, error) {
	region, ok := m.regions[id]
	if !ok || region.DeletedAt != nil {
		return models.TaxRegion{}, errors.NotFound("tax_region_not_found", "vergi bölgesi bulunamadı: %s", id)
	}
	return region, nil
}

// GetTaxRegionsByIDs verilen kimliklerin canlı bölgelerini döner.
func (m *memRepo) GetTaxRegionsByIDs(_ context.Context, ids []string) ([]models.TaxRegion, error) {
	out := make([]models.TaxRegion, 0, len(ids))
	for _, id := range ids {
		if region, ok := m.regions[id]; ok && region.DeletedAt == nil {
			out = append(out, region)
		}
	}
	return out, nil
}

// ListTaxRegions sayfalanmış bölge listesini döner.
func (m *memRepo) ListTaxRegions(_ context.Context, countryCode string, limit, offset int32) ([]models.TaxRegion, int64, error) {
	all := make([]models.TaxRegion, 0, len(m.regions))
	// Anahtar üzerinden dolaşılır: değerle dolaşmak her turda modelin
	// tamamını kopyalar.
	for key := range m.regions {
		region := m.regions[key]
		if region.DeletedAt != nil {
			continue
		}
		if countryCode != "" && region.CountryCode != countryCode {
			continue
		}
		all = append(all, region)
	}
	slices.SortFunc(all, func(a, b models.TaxRegion) int { return compare(a.ID, b.ID) })

	total := int64(len(all))
	if int(offset) >= len(all) {
		return []models.TaxRegion{}, total, nil
	}
	end := min(int(offset)+int(limit), len(all))
	return all[offset:end], total, nil
}

// ResolveTaxRegions ülkenin kökünü ve (verilmişse) eyaletini döner.
func (m *memRepo) ResolveTaxRegions(_ context.Context, countryCode, provinceCode string) ([]models.TaxRegion, error) {
	var province, root []models.TaxRegion
	// Anahtar üzerinden dolaşılır: değerle dolaşmak her turda modelin
	// tamamını kopyalar.
	for key := range m.regions {
		region := m.regions[key]
		if region.DeletedAt != nil || region.CountryCode != countryCode {
			continue
		}
		switch {
		case region.IsRoot():
			root = append(root, region)
		case provinceCode != "" && region.Province() == provinceCode:
			province = append(province, region)
		}
	}
	return append(province, root...), nil
}

// DeleteTaxRegion bölgeyi ve alt kayıtlarını siler.
func (m *memRepo) DeleteTaxRegion(_ context.Context, id string, now time.Time) error {
	region, ok := m.regions[id]
	if !ok || region.DeletedAt != nil {
		return errors.NotFound("tax_region_not_found", "vergi bölgesi bulunamadı: %s", id)
	}

	deleted := now.UTC()
	region.DeletedAt = &deleted
	m.regions[id] = region
	// Anahtar üzerinden dolaşılır: değerle dolaşmak her turda modelin
	// tamamını kopyalar.
	for rateID := range m.rates {
		rate := m.rates[rateID]
		if rate.DeletedAt == nil && rate.TaxRegionID == id {
			rate.DeletedAt = &deleted
			m.rates[rateID] = rate
		}
	}
	return nil
}

// CreateTaxRate oranı yazar; bölgenin ikinci varsayılanını reddeder.
func (m *memRepo) CreateTaxRate(_ context.Context, rate models.TaxRate, now time.Time) (models.TaxRate, error) {
	if rate.IsDefault {
		// Anahtar üzerinden dolaşılır: değerle dolaşmak her turda modelin
		// tamamını kopyalar.
		for key := range m.rates {
			existing := m.rates[key]
			if existing.DeletedAt == nil && existing.TaxRegionID == rate.TaxRegionID && existing.IsDefault {
				return models.TaxRate{}, errors.Conflict("tax_duplicate", "varsayılan oran zaten var")
			}
		}
	}
	rate.CreatedAt = now.UTC()
	rate.UpdatedAt = now.UTC()
	m.rates[rate.ID] = rate
	return rate, nil
}

// GetTaxRate kimliğe göre canlı oranı döner.
func (m *memRepo) GetTaxRate(_ context.Context, id string) (models.TaxRate, error) {
	rate, ok := m.rates[id]
	if !ok || rate.DeletedAt != nil {
		return models.TaxRate{}, errors.NotFound("tax_rate_not_found", "vergi oranı bulunamadı: %s", id)
	}
	return rate, nil
}

// ListTaxRates bir bölgenin canlı oranlarını döner.
func (m *memRepo) ListTaxRates(_ context.Context, regionID string) ([]models.TaxRate, error) {
	return m.ratesFor([]string{regionID}), nil
}

// ListTaxRatesByRegions birden çok bölgenin canlı oranlarını döner.
func (m *memRepo) ListTaxRatesByRegions(_ context.Context, regionIDs []string) ([]models.TaxRate, error) {
	return m.ratesFor(regionIDs), nil
}

// ratesFor verilen bölgelerin oranlarını sıralı döner.
func (m *memRepo) ratesFor(regionIDs []string) []models.TaxRate {
	out := make([]models.TaxRate, 0, len(m.rates))
	// Anahtar üzerinden dolaşılır: değerle dolaşmak her turda modelin
	// tamamını kopyalar.
	for key := range m.rates {
		rate := m.rates[key]
		if rate.DeletedAt == nil && slices.Contains(regionIDs, rate.TaxRegionID) {
			out = append(out, rate)
		}
	}
	slices.SortFunc(out, func(a, b models.TaxRate) int {
		if a.IsDefault != b.IsDefault {
			if a.IsDefault {
				return -1
			}
			return 1
		}
		return compare(a.ID, b.ID)
	})
	return out
}

// UpdateTaxRate yamayı uygular.
func (m *memRepo) UpdateTaxRate(_ context.Context, id string, patch models.TaxRatePatch, now time.Time) (models.TaxRate, error) {
	current, ok := m.rates[id]
	if !ok || current.DeletedAt != nil {
		return models.TaxRate{}, errors.NotFound("tax_rate_not_found", "vergi oranı bulunamadı: %s", id)
	}
	updated := current.Patched(patch)
	updated.UpdatedAt = now.UTC()
	m.rates[id] = updated
	return updated, nil
}

// DeleteTaxRate oranı ve kurallarını siler.
func (m *memRepo) DeleteTaxRate(_ context.Context, id string, now time.Time) error {
	rate, ok := m.rates[id]
	if !ok || rate.DeletedAt != nil {
		return errors.NotFound("tax_rate_not_found", "vergi oranı bulunamadı: %s", id)
	}
	deleted := now.UTC()
	rate.DeletedAt = &deleted
	m.rates[id] = rate
	// Anahtar üzerinden dolaşılır: değerle dolaşmak her turda modelin
	// tamamını kopyalar.
	for ruleID := range m.rules {
		rule := m.rules[ruleID]
		if rule.DeletedAt == nil && rule.TaxRateID == id {
			rule.DeletedAt = &deleted
			m.rules[ruleID] = rule
		}
	}
	return nil
}

// CreateTaxRateRule kuralı yazar; varsayılan orana kural eklemeyi reddeder.
func (m *memRepo) CreateTaxRateRule(_ context.Context, rule models.TaxRateRule, now time.Time) (models.TaxRateRule, error) {
	rate, ok := m.rates[rule.TaxRateID]
	if !ok || rate.DeletedAt != nil {
		return models.TaxRateRule{}, errors.NotFound("tax_rate_not_found",
			"vergi oranı bulunamadı: %s", rule.TaxRateID)
	}
	if rate.IsDefault {
		return models.TaxRateRule{}, errors.Conflict("tax_constraint_violation",
			"varsayılan oranın kuralı olamaz: %s", rule.TaxRateID)
	}
	rule.CreatedAt = now.UTC()
	rule.UpdatedAt = now.UTC()
	m.rules[rule.ID] = rule
	return rule, nil
}

// GetTaxRateRule kimliğe göre canlı kuralı döner.
func (m *memRepo) GetTaxRateRule(_ context.Context, id string) (models.TaxRateRule, error) {
	rule, ok := m.rules[id]
	if !ok || rule.DeletedAt != nil {
		return models.TaxRateRule{}, errors.NotFound("tax_rate_rule_not_found",
			"vergi kuralı bulunamadı: %s", id)
	}
	return rule, nil
}

// ListTaxRateRules bir oranın canlı kurallarını döner.
func (m *memRepo) ListTaxRateRules(_ context.Context, rateID string) ([]models.TaxRateRule, error) {
	return m.rulesFor([]string{rateID}), nil
}

// ListTaxRateRulesByRates birden çok oranın canlı kurallarını döner.
func (m *memRepo) ListTaxRateRulesByRates(_ context.Context, rateIDs []string) ([]models.TaxRateRule, error) {
	return m.rulesFor(rateIDs), nil
}

// rulesFor verilen oranların kurallarını sıralı döner.
func (m *memRepo) rulesFor(rateIDs []string) []models.TaxRateRule {
	out := make([]models.TaxRateRule, 0, len(m.rules))
	// Anahtar üzerinden dolaşılır: değerle dolaşmak her turda modelin
	// tamamını kopyalar.
	for key := range m.rules {
		rule := m.rules[key]
		if rule.DeletedAt == nil && slices.Contains(rateIDs, rule.TaxRateID) {
			out = append(out, rule)
		}
	}
	slices.SortFunc(out, func(a, b models.TaxRateRule) int { return compare(a.ID, b.ID) })
	return out
}

// DeleteTaxRateRule kuralı siler.
func (m *memRepo) DeleteTaxRateRule(_ context.Context, id string, now time.Time) error {
	rule, ok := m.rules[id]
	if !ok || rule.DeletedAt != nil {
		return errors.NotFound("tax_rate_rule_not_found", "vergi kuralı bulunamadı: %s", id)
	}
	deleted := now.UTC()
	rule.DeletedAt = &deleted
	m.rules[id] = rule
	return nil
}

// compare iki dizeyi sözlüksel olarak karşılaştırır.
func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
