package service

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// memRepo [Repository]'nin bellek içi uygulamasıdır.
//
// Amacı, servisin KURALLARINI veritabanı olmadan doğrulayabilmektir: kimlik
// üretimi, normalleştirme, oran seçimi, yuvarlama, hata sınıflandırması ve
// yapılan SORGU SAYISI. Veritabanına özgü iddialar (kısmi benzersiz indeksin
// ikinci kök bölgeyi reddetmesi, bileşik foreign key'in eyalet-ülke
// tutarsızlığını engellemesi, satır kilidinin eşzamanlı iki güncellemeyi
// ayırması) BURADA DEĞİL, entegrasyon testlerinde kanıtlanır — sahte bir depo
// kendi yazdığı kuralı doğrulayamaz.
//
// Yine de aynı kısıtların bellek içi karşılıkları uygulanır: servis o
// hataların SINIFINA göre dallanır ve sahte depo onları hiç üretmeseydi
// servisin çakışma yolları hiç sınanmazdı.
//
// Depo çağrıları sayılır (calls): "kalem başına sorgu yapılmıyor" iddiasının
// kanıtı budur.
type memRepo struct {
	mu sync.Mutex

	regions map[string]models.TaxRegion
	rates   map[string]models.TaxRate
	rules   map[string]models.TaxRateRule

	// calls metot adına göre çağrı sayacıdır.
	calls map[string]int
	// failOn dolu bir metot adı için o çağrının döneceği hatadır.
	failOn map[string]error
}

var _ Repository = (*memRepo)(nil)
var _ RateSource = (*memRepo)(nil)

// newMemRepo boş bir bellek içi depo üretir.
func newMemRepo() *memRepo {
	return &memRepo{
		regions: map[string]models.TaxRegion{},
		rates:   map[string]models.TaxRate{},
		rules:   map[string]models.TaxRateRule{},
		calls:   map[string]int{},
		failOn:  map[string]error{},
	}
}

// enter çağrıyı sayar ve betiklenmiş hatayı döner.
func (m *memRepo) enter(name string) error {
	m.calls[name]++
	if err, ok := m.failOn[name]; ok {
		return err
	}
	return nil
}

// CreateTaxRegion bölgeyi yazar; ülkenin ikinci kökünü reddeder.
func (m *memRepo) CreateTaxRegion(_ context.Context, region models.TaxRegion, now time.Time) (models.TaxRegion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("CreateTaxRegion"); err != nil {
		return models.TaxRegion{}, err
	}

	// Anahtar üzerinden dolaşılır: değerle dolaşmak her turda modelin
	// tamamını kopyalar.
	for key := range m.regions {
		existing := m.regions[key]
		if existing.DeletedAt != nil || existing.CountryCode != region.CountryCode {
			continue
		}
		if region.IsRoot() && existing.IsRoot() {
			// Kod, gerçek deponun kısıt adına göre ürettiğiyle AYNIDIR
			// (repository.duplicateCode); sahte depo genel "tax_duplicate"i
			// dönseydi servisin gördüğü hata gerçekte olduğundan farklı olurdu.
			return models.TaxRegion{}, errors.Conflict(CodeRootExists,
				"kök bölge zaten var (kısıt: tax_region_country_root_uniq)")
		}
		if !region.IsRoot() && !existing.IsRoot() &&
			existing.Parent() == region.Parent() && existing.Province() == region.Province() {
			return models.TaxRegion{}, errors.Conflict("tax_duplicate",
				"eyalet bölgesi zaten var (kısıt: tax_region_province_uniq)")
		}
	}
	if !region.IsRoot() {
		parent, ok := m.regions[region.Parent()]
		if !ok || parent.DeletedAt != nil || parent.CountryCode != region.CountryCode {
			return models.TaxRegion{}, errors.Invalid("tax_constraint_violation",
				"ebeveyn bulunamadı (kısıt: tax_region_parent_fk)")
		}
	}

	region.CreatedAt = now.UTC()
	region.UpdatedAt = now.UTC()
	m.regions[region.ID] = region
	return region, nil
}

// GetTaxRegion kimliğe göre canlı bölgeyi döner.
func (m *memRepo) GetTaxRegion(_ context.Context, id string) (models.TaxRegion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("GetTaxRegion"); err != nil {
		return models.TaxRegion{}, err
	}

	region, ok := m.regions[id]
	if !ok || region.DeletedAt != nil {
		return models.TaxRegion{}, errors.NotFound("tax_region_not_found",
			"vergi bölgesi bulunamadı: %s", id)
	}
	return region, nil
}

// GetTaxRegionsByIDs verilen kimliklerin canlı bölgelerini döner.
func (m *memRepo) GetTaxRegionsByIDs(_ context.Context, ids []string) ([]models.TaxRegion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("GetTaxRegionsByIDs"); err != nil {
		return nil, err
	}

	out := make([]models.TaxRegion, 0, len(ids))
	for _, id := range ids {
		if region, ok := m.regions[id]; ok && region.DeletedAt == nil {
			out = append(out, region)
		}
	}
	sortRegions(out)
	return out, nil
}

// ListTaxRegions sayfalanmış bölge listesini döner.
func (m *memRepo) ListTaxRegions(_ context.Context, countryCode string, limit, offset int32) ([]models.TaxRegion, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ListTaxRegions"); err != nil {
		return nil, 0, err
	}

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
	sortRegions(all)

	total := int64(len(all))
	if int(offset) >= len(all) {
		return []models.TaxRegion{}, total, nil
	}
	end := min(int(offset)+int(limit), len(all))
	return slices.Clone(all[offset:end]), total, nil
}

// ResolveTaxRegions ülkenin kökünü ve (verilmişse) eyaletini döner; eyalet
// ÖNCE gelir.
func (m *memRepo) ResolveTaxRegions(_ context.Context, countryCode, provinceCode string) ([]models.TaxRegion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ResolveTaxRegions"); err != nil {
		return nil, err
	}

	var root, province []models.TaxRegion
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
	sortRegions(root)
	sortRegions(province)
	return append(province, root...), nil
}

// DeleteTaxRegion bölgeyi, alt bölgelerini, oranlarını ve kurallarını siler.
func (m *memRepo) DeleteTaxRegion(_ context.Context, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("DeleteTaxRegion"); err != nil {
		return err
	}

	region, ok := m.regions[id]
	if !ok || region.DeletedAt != nil {
		return errors.NotFound("tax_region_not_found", "vergi bölgesi bulunamadı: %s", id)
	}

	deleted := now.UTC()
	regionIDs := map[string]bool{}
	// Anahtar üzerinden dolaşılır: değerle dolaşmak her turda modelin
	// tamamını kopyalar.
	for regionID := range m.regions {
		candidate := m.regions[regionID]
		if candidate.DeletedAt != nil {
			continue
		}
		if regionID != id && candidate.Parent() != id {
			continue
		}
		candidate.DeletedAt = &deleted
		candidate.UpdatedAt = deleted
		m.regions[regionID] = candidate
		regionIDs[regionID] = true
	}

	rateIDs := map[string]bool{}
	// Anahtar üzerinden dolaşılır: değerle dolaşmak her turda modelin
	// tamamını kopyalar.
	for rateID := range m.rates {
		rate := m.rates[rateID]
		if rate.DeletedAt != nil || !regionIDs[rate.TaxRegionID] {
			continue
		}
		rate.DeletedAt = &deleted
		rate.UpdatedAt = deleted
		m.rates[rateID] = rate
		rateIDs[rateID] = true
	}
	// Anahtar üzerinden dolaşılır: değerle dolaşmak her turda modelin
	// tamamını kopyalar.
	for ruleID := range m.rules {
		rule := m.rules[ruleID]
		if rule.DeletedAt != nil || !rateIDs[rule.TaxRateID] {
			continue
		}
		rule.DeletedAt = &deleted
		rule.UpdatedAt = deleted
		m.rules[ruleID] = rule
	}
	return nil
}

// CreateTaxRate oranı yazar; bölgenin ikinci varsayılanını reddeder.
func (m *memRepo) CreateTaxRate(_ context.Context, rate models.TaxRate, now time.Time) (models.TaxRate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("CreateTaxRate"); err != nil {
		return models.TaxRate{}, err
	}

	region, ok := m.regions[rate.TaxRegionID]
	if !ok || region.DeletedAt != nil {
		return models.TaxRate{}, errors.Invalid("tax_constraint_violation",
			"bölge bulunamadı (kısıt: tax_rate_region_fk)")
	}
	if rate.IsDefault && m.defaultRateLocked(rate.TaxRegionID) != "" {
		// Kod gerçek deponunkiyle aynıdır; bkz. CreateTaxRegion.
		return models.TaxRate{}, errors.Conflict(CodeDefaultExists,
			"varsayılan oran zaten var (kısıt: tax_rate_default_uniq)")
	}

	rate.CreatedAt = now.UTC()
	rate.UpdatedAt = now.UTC()
	m.rates[rate.ID] = rate
	return rate, nil
}

// defaultRateLocked bölgenin varsayılan oran kimliğini döner; çağıran kilidi
// tutuyor olmalıdır.
func (m *memRepo) defaultRateLocked(regionID string) string {
	// Anahtar üzerinden dolaşılır: değerle dolaşmak her turda modelin
	// tamamını kopyalar.
	for id := range m.rates {
		rate := m.rates[id]
		if rate.DeletedAt == nil && rate.TaxRegionID == regionID && rate.IsDefault {
			return id
		}
	}
	return ""
}

// GetTaxRate kimliğe göre canlı oranı döner.
func (m *memRepo) GetTaxRate(_ context.Context, id string) (models.TaxRate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("GetTaxRate"); err != nil {
		return models.TaxRate{}, err
	}

	rate, ok := m.rates[id]
	if !ok || rate.DeletedAt != nil {
		return models.TaxRate{}, errors.NotFound("tax_rate_not_found", "vergi oranı bulunamadı: %s", id)
	}
	return rate, nil
}

// ListTaxRates bir bölgenin canlı oranlarını döner.
func (m *memRepo) ListTaxRates(_ context.Context, regionID string) ([]models.TaxRate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ListTaxRates"); err != nil {
		return nil, err
	}
	return m.ratesForLocked([]string{regionID}), nil
}

// ListTaxRatesByRegions birden çok bölgenin canlı oranlarını döner.
func (m *memRepo) ListTaxRatesByRegions(_ context.Context, regionIDs []string) ([]models.TaxRate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ListTaxRatesByRegions"); err != nil {
		return nil, err
	}
	return m.ratesForLocked(regionIDs), nil
}

// ratesForLocked verilen bölgelerin canlı oranlarını sıralı döner; çağıran
// kilidi tutuyor olmalıdır.
func (m *memRepo) ratesForLocked(regionIDs []string) []models.TaxRate {
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
		if a.TaxRegionID != b.TaxRegionID {
			return compareStrings(a.TaxRegionID, b.TaxRegionID)
		}
		if a.IsDefault != b.IsDefault {
			if a.IsDefault {
				return -1
			}
			return 1
		}
		return compareStrings(a.ID, b.ID)
	})
	return out
}

// UpdateTaxRate yamayı uygular; varsayılanlık kısıtlarını denetler.
func (m *memRepo) UpdateTaxRate(_ context.Context, id string, patch models.TaxRatePatch, now time.Time) (models.TaxRate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("UpdateTaxRate"); err != nil {
		return models.TaxRate{}, err
	}

	current, ok := m.rates[id]
	if !ok || current.DeletedAt != nil {
		return models.TaxRate{}, errors.NotFound("tax_rate_not_found", "vergi oranı bulunamadı: %s", id)
	}

	updated := current.Patched(patch)
	if updated.IsDefault && !current.IsDefault {
		if m.ruleCountLocked(id) > 0 {
			return models.TaxRate{}, errors.Conflict("tax_constraint_violation",
				"kurallı bir oran varsayılan yapılamaz: %s", id)
		}
		if other := m.defaultRateLocked(current.TaxRegionID); other != "" && other != id {
			return models.TaxRate{}, errors.Conflict(CodeDefaultExists,
				"varsayılan oran zaten var (kısıt: tax_rate_default_uniq)")
		}
	}

	updated.UpdatedAt = now.UTC()
	m.rates[id] = updated
	return updated, nil
}

// ruleCountLocked oranın canlı kural sayısını döner; çağıran kilidi tutuyor
// olmalıdır.
func (m *memRepo) ruleCountLocked(rateID string) int {
	count := 0
	// Anahtar üzerinden dolaşılır: değerle dolaşmak her turda modelin
	// tamamını kopyalar.
	for key := range m.rules {
		rule := m.rules[key]
		if rule.DeletedAt == nil && rule.TaxRateID == rateID {
			count++
		}
	}
	return count
}

// DeleteTaxRate oranı ve kurallarını siler.
func (m *memRepo) DeleteTaxRate(_ context.Context, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("DeleteTaxRate"); err != nil {
		return err
	}

	rate, ok := m.rates[id]
	if !ok || rate.DeletedAt != nil {
		return errors.NotFound("tax_rate_not_found", "vergi oranı bulunamadı: %s", id)
	}

	deleted := now.UTC()
	rate.DeletedAt = &deleted
	rate.UpdatedAt = deleted
	m.rates[id] = rate

	// Anahtar üzerinden dolaşılır: değerle dolaşmak her turda modelin
	// tamamını kopyalar.
	for ruleID := range m.rules {
		rule := m.rules[ruleID]
		if rule.DeletedAt == nil && rule.TaxRateID == id {
			rule.DeletedAt = &deleted
			rule.UpdatedAt = deleted
			m.rules[ruleID] = rule
		}
	}
	return nil
}

// CreateTaxRateRule kuralı yazar; varsayılan orana kural eklemeyi reddeder.
func (m *memRepo) CreateTaxRateRule(_ context.Context, rule models.TaxRateRule, now time.Time) (models.TaxRateRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("CreateTaxRateRule"); err != nil {
		return models.TaxRateRule{}, err
	}

	rate, ok := m.rates[rule.TaxRateID]
	if !ok || rate.DeletedAt != nil {
		return models.TaxRateRule{}, errors.NotFound("tax_rate_not_found",
			"vergi oranı bulunamadı: %s", rule.TaxRateID)
	}
	if rate.IsDefault {
		return models.TaxRateRule{}, errors.Conflict("tax_constraint_violation",
			"varsayılan oranın kuralı olamaz: %s", rule.TaxRateID)
	}
	// Anahtar üzerinden dolaşılır: değerle dolaşmak her turda modelin
	// tamamını kopyalar.
	for key := range m.rules {
		existing := m.rules[key]
		if existing.DeletedAt == nil && existing.TaxRateID == rule.TaxRateID &&
			existing.Reference == rule.Reference && existing.ReferenceID == rule.ReferenceID {
			return models.TaxRateRule{}, errors.Conflict("tax_duplicate",
				"kural zaten var (kısıt: tax_rate_rule_uniq)")
		}
	}

	rule.CreatedAt = now.UTC()
	rule.UpdatedAt = now.UTC()
	m.rules[rule.ID] = rule
	return rule, nil
}

// GetTaxRateRule kimliğe göre canlı kuralı döner.
func (m *memRepo) GetTaxRateRule(_ context.Context, id string) (models.TaxRateRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("GetTaxRateRule"); err != nil {
		return models.TaxRateRule{}, err
	}

	rule, ok := m.rules[id]
	if !ok || rule.DeletedAt != nil {
		return models.TaxRateRule{}, errors.NotFound("tax_rate_rule_not_found",
			"vergi kuralı bulunamadı: %s", id)
	}
	return rule, nil
}

// ListTaxRateRules bir oranın canlı kurallarını döner.
func (m *memRepo) ListTaxRateRules(_ context.Context, rateID string) ([]models.TaxRateRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ListTaxRateRules"); err != nil {
		return nil, err
	}
	return m.rulesForLocked([]string{rateID}), nil
}

// ListTaxRateRulesByRates birden çok oranın canlı kurallarını döner.
func (m *memRepo) ListTaxRateRulesByRates(_ context.Context, rateIDs []string) ([]models.TaxRateRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ListTaxRateRulesByRates"); err != nil {
		return nil, err
	}
	return m.rulesForLocked(rateIDs), nil
}

// rulesForLocked verilen oranların canlı kurallarını sıralı döner.
func (m *memRepo) rulesForLocked(rateIDs []string) []models.TaxRateRule {
	out := make([]models.TaxRateRule, 0, len(m.rules))
	// Anahtar üzerinden dolaşılır: değerle dolaşmak her turda modelin
	// tamamını kopyalar.
	for key := range m.rules {
		rule := m.rules[key]
		if rule.DeletedAt == nil && slices.Contains(rateIDs, rule.TaxRateID) {
			out = append(out, rule)
		}
	}
	slices.SortFunc(out, func(a, b models.TaxRateRule) int {
		if a.TaxRateID != b.TaxRateID {
			return compareStrings(a.TaxRateID, b.TaxRateID)
		}
		return compareStrings(a.ID, b.ID)
	})
	return out
}

// DeleteTaxRateRule kuralı siler.
func (m *memRepo) DeleteTaxRateRule(_ context.Context, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("DeleteTaxRateRule"); err != nil {
		return err
	}

	rule, ok := m.rules[id]
	if !ok || rule.DeletedAt != nil {
		return errors.NotFound("tax_rate_rule_not_found", "vergi kuralı bulunamadı: %s", id)
	}

	deleted := now.UTC()
	rule.DeletedAt = &deleted
	rule.UpdatedAt = deleted
	m.rules[id] = rule
	return nil
}

// callCount bir metodun çağrı sayısını döner.
func (m *memRepo) callCount(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[name]
}

// sortRegions bölgeleri belirlenimci sıraya koyar: kök ÖNCE, sonra kimlik.
//
// Gerçek sorgudaki sıra ile aynı olması şart değildir; şart olan, sahte deponun
// harita dolaşım sırasına GÖRE DEĞİŞMEMESİDİR. Aksi hâlde testler kendi
// içinde kararsız olurdu.
func sortRegions(regions []models.TaxRegion) {
	slices.SortFunc(regions, func(a, b models.TaxRegion) int {
		if a.IsRoot() != b.IsRoot() {
			if a.IsRoot() {
				return -1
			}
			return 1
		}
		return compareStrings(a.ID, b.ID)
	})
}
