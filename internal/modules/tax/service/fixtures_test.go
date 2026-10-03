package service

import (
	"testing"
	"time"

	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// testNow is the tests' fixed clock; time-dependent fields become
// deterministic.
var testNow = time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)

// The fixed ids used in the tests.
//
// The ids are chosen BY HAND because the rate selection's tie-break rule ("the
// one with the smaller id wins") can be tested only with ids whose order is
// known; since the generator produces a random body, that rule could not be
// proven with generated ids.
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

// newTestService builds a service that runs on an in-memory repository.
func newTestService(t *testing.T) (*Service, *memRepo) {
	t.Helper()

	repo := newMemRepo()
	svc := New(repo, Options{Now: func() time.Time { return testNow }})
	return svc, repo
}

// seedRegion writes a region directly into the repository and returns it.
//
// It SKIPS the service's validations: the aim is to set up the DATA the
// calculation rests on, and the rules of the path that leads there are tested
// separately.
func (m *memRepo) seedRegion(region models.TaxRegion) models.TaxRegion {
	m.mu.Lock()
	defer m.mu.Unlock()

	region.CreatedAt = testNow
	region.UpdatedAt = testNow
	m.regions[region.ID] = region
	return region
}

// seedRootRegion writes a country root.
func (m *memRepo) seedRootRegion(id, countryCode string) models.TaxRegion {
	return m.seedRegion(models.TaxRegion{ID: id, CountryCode: countryCode})
}

// seedProvinceRegion writes a province region under a root.
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

// seedRate writes a rate directly into the repository.
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

// seedDefaultRate writes a default rate into a region.
func (m *memRepo) seedDefaultRate(id, regionID string, rateBps int32) models.TaxRate {
	return m.seedRate(models.TaxRate{
		ID: id, TaxRegionID: regionID, Name: "default", RateBps: rateBps, IsDefault: true,
	})
}

// seedRuledRate writes a ruled rate into a region (its rules are added
// separately).
func (m *memRepo) seedRuledRate(id, regionID string, rateBps int32) models.TaxRate {
	return m.seedRate(models.TaxRate{
		ID: id, TaxRegionID: regionID, Name: "ruled", RateBps: rateBps,
	})
}

// seedRule writes a rule directly into the repository.
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
