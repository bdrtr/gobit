package service

import (
	"testing"
	"time"

	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// testNow testlerin sabit saatidir; zamana bağlı alanlar belirlenimci olur.
var testNow = time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)

// Testlerde kullanılan sabit kimlikler.
//
// Kimlikler ELLE seçilir çünkü oran seçiminin eşitlik bozma kuralı ("kimliği
// küçük olan kazanır") ancak sıraları bilinen kimliklerle sınanabilir; üretici
// rastgele gövde ürettiği için o kural üretilmiş kimliklerle kanıtlanamazdı.
const (
	trRegionID = models.TaxRegionIDPrefix + "TR0000000000000000000000000"
	trIstanbul = models.TaxRegionIDPrefix + "TR34000000000000000000000000"
	usRegionID = models.TaxRegionIDPrefix + "US0000000000000000000000000"

	rateA = models.TaxRateIDPrefix + "A0000000000000000000000000"
	rateB = models.TaxRateIDPrefix + "B0000000000000000000000000"
	rateC = models.TaxRateIDPrefix + "C0000000000000000000000000"
	rateD = models.TaxRateIDPrefix + "D0000000000000000000000000"

	ruleA = models.TaxRateRuleIDPrefix + "A0000000000000000000000000"
	ruleB = models.TaxRateRuleIDPrefix + "B0000000000000000000000000"
	ruleC = models.TaxRateRuleIDPrefix + "C0000000000000000000000000"
)

// newTestService bellek içi depo üzerinde çalışan bir servis kurar.
func newTestService(t *testing.T) (*Service, *memRepo) {
	t.Helper()

	repo := newMemRepo()
	svc := New(repo, Options{Now: func() time.Time { return testNow }})
	return svc, repo
}

// seedRegion depoya doğrudan bir bölge yazar ve kimliğini döner.
//
// Servisin doğrulamalarını ATLAR: amaç, hesabın dayandığı VERİYİ kurmaktır ve
// oraya giden yolun kuralları ayrı testlerde sınanır.
func (m *memRepo) seedRegion(region models.TaxRegion) models.TaxRegion {
	m.mu.Lock()
	defer m.mu.Unlock()

	region.CreatedAt = testNow
	region.UpdatedAt = testNow
	m.regions[region.ID] = region
	return region
}

// seedRootRegion bir ülke kökü yazar.
func (m *memRepo) seedRootRegion(id, countryCode string) models.TaxRegion {
	return m.seedRegion(models.TaxRegion{ID: id, CountryCode: countryCode})
}

// seedProvinceRegion bir kökün altına eyalet bölgesi yazar.
func (m *memRepo) seedProvinceRegion(id, countryCode, provinceCode, parentID string) models.TaxRegion {
	province := provinceCode
	parent := parentID
	return m.seedRegion(models.TaxRegion{
		ID:           id,
		CountryCode:  countryCode,
		ProvinceCode: &province,
		ParentID:     &parent,
	})
}

// seedRate depoya doğrudan bir oran yazar.
func (m *memRepo) seedRate(rate models.TaxRate) models.TaxRate {
	m.mu.Lock()
	defer m.mu.Unlock()

	if rate.Name == "" {
		rate.Name = "test"
	}
	rate.CreatedAt = testNow
	rate.UpdatedAt = testNow
	m.rates[rate.ID] = rate
	return rate
}

// seedDefaultRate bir bölgeye varsayılan oran yazar.
func (m *memRepo) seedDefaultRate(id, regionID string, rateBps int32) models.TaxRate {
	return m.seedRate(models.TaxRate{
		ID: id, TaxRegionID: regionID, Name: "varsayılan", RateBps: rateBps, IsDefault: true,
	})
}

// seedRuledRate bir bölgeye kurallı oran yazar (kuralları ayrıca eklenir).
func (m *memRepo) seedRuledRate(id, regionID string, rateBps int32) models.TaxRate {
	return m.seedRate(models.TaxRate{
		ID: id, TaxRegionID: regionID, Name: "kurallı", RateBps: rateBps,
	})
}

// seedRule depoya doğrudan bir kural yazar.
func (m *memRepo) seedRule(id, rateID string, reference models.RuleReference, referenceID string) models.TaxRateRule {
	m.mu.Lock()
	defer m.mu.Unlock()

	rule := models.TaxRateRule{
		ID:          id,
		TaxRateID:   rateID,
		Reference:   reference,
		ReferenceID: referenceID,
		CreatedAt:   testNow,
		UpdatedAt:   testNow,
	}
	m.rules[id] = rule
	return rule
}
