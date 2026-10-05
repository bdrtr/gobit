package api_test

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// memRepo is an in-memory implementation of [service.Repository].
//
// The HTTP layer tests use the REAL service; only the repository is faked. That
// way validation, error classification and the envelope shape are tested end
// to end, and it can be proven that the handlers do NOT CHOOSE the status code
// (core/http does).
type memRepo struct {
	regions map[string]models.TaxRegion
	rates   map[string]models.TaxRate
	rules   map[string]models.TaxRateRule
	classes map[string]models.TaxClass
	// members binds a product id to a class id: a product is in AT MOST one
	// class, the same as the partial unique index in the schema.
	members map[string]string
}

var _ service.Repository = (*memRepo)(nil)

// newMemRepo builds an empty in-memory repository.
func newMemRepo() *memRepo {
	return &memRepo{
		regions: map[string]models.TaxRegion{},
		classes: map[string]models.TaxClass{},
		members: map[string]string{},
		rates:   map[string]models.TaxRate{},
		rules:   map[string]models.TaxRateRule{},
	}
}

// WithTx runs fn AS IS; it does NOT ROLL BACK.
//
// This file's subject is the HTTP layer: the status code, the envelope shape
// and the error mapping. That the transaction really rolls back is shown not
// here but in the integration tests that run on a real database; a convincing
// imitation of a rollback in an in-memory repository would hide a write the
// database does not roll back.
func (m *memRepo) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

// LockTaxRegion returns the live region; there is NO lock (see
// [memRepo.WithTx]).
func (m *memRepo) LockTaxRegion(ctx context.Context, id string) (models.TaxRegion, error) {
	return m.GetTaxRegion(ctx, id)
}

// LockTaxRegionForWrite returns the live region; there is NO lock (see
// [memRepo.WithTx]).
func (m *memRepo) LockTaxRegionForWrite(ctx context.Context, id string) (models.TaxRegion, error) {
	return m.GetTaxRegion(ctx, id)
}

// CreateTaxRegion writes the region; it rejects a country's second root.
func (m *memRepo) CreateTaxRegion(_ context.Context, region models.TaxRegion, now time.Time) (models.TaxRegion, error) {
	// The loop runs over the keys: ranging over the values would copy the
	// whole model on every iteration.
	for key := range m.regions {
		existing := m.regions[key]
		if existing.DeletedAt == nil && existing.CountryCode == region.CountryCode &&
			existing.IsRoot() && region.IsRoot() {
			return models.TaxRegion{}, errors.Conflict("tax_duplicate", "a root region already exists")
		}
	}
	region.CreatedAt = now.UTC()
	region.UpdatedAt = now.UTC()
	m.regions[region.ID] = region
	return region, nil
}

// GetTaxRegion returns the live region by id.
func (m *memRepo) GetTaxRegion(_ context.Context, id string) (models.TaxRegion, error) {
	region, ok := m.regions[id]
	if !ok || region.DeletedAt != nil {
		return models.TaxRegion{}, errors.NotFound("tax_region_not_found", "tax region not found: %s", id)
	}
	return region, nil
}

// GetTaxRegionsByIDs returns the live regions of the given ids.
func (m *memRepo) GetTaxRegionsByIDs(_ context.Context, ids []string) ([]models.TaxRegion, error) {
	out := make([]models.TaxRegion, 0, len(ids))
	for _, id := range ids {
		if region, ok := m.regions[id]; ok && region.DeletedAt == nil {
			out = append(out, region)
		}
	}
	return out, nil
}

// ListTaxRegions returns the paged list of regions.
func (m *memRepo) ListTaxRegions(_ context.Context, countryCode string, limit, offset int32) ([]models.TaxRegion, int64, error) {
	all := make([]models.TaxRegion, 0, len(m.regions))
	// The loop runs over the keys: ranging over the values would copy the
	// whole model on every iteration.
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

// ResolveTaxRegions returns the country's root and (if given) its province.
func (m *memRepo) ResolveTaxRegions(_ context.Context, countryCode, provinceCode string) ([]models.TaxRegion, error) {
	var province, root []models.TaxRegion
	// The loop runs over the keys: ranging over the values would copy the
	// whole model on every iteration.
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

// DeleteTaxRegion deletes the region and its child records.
func (m *memRepo) DeleteTaxRegion(_ context.Context, id string, now time.Time) error {
	region, ok := m.regions[id]
	if !ok || region.DeletedAt != nil {
		return errors.NotFound("tax_region_not_found", "tax region not found: %s", id)
	}

	deleted := now.UTC()
	region.DeletedAt = &deleted
	m.regions[id] = region
	// The loop runs over the keys: ranging over the values would copy the
	// whole model on every iteration.
	for rateID := range m.rates {
		rate := m.rates[rateID]
		if rate.DeletedAt == nil && rate.TaxRegionID == id {
			rate.DeletedAt = &deleted
			m.rates[rateID] = rate
		}
	}
	return nil
}

// CreateTaxRate writes the rate; it rejects a region's second default.
func (m *memRepo) CreateTaxRate(_ context.Context, rate models.TaxRate, now time.Time) (models.TaxRate, error) {
	if rate.IsDefault {
		// The loop runs over the keys: ranging over the values would copy
		// the whole model on every iteration.
		for key := range m.rates {
			existing := m.rates[key]
			if existing.DeletedAt == nil && existing.TaxRegionID == rate.TaxRegionID && existing.IsDefault {
				return models.TaxRate{}, errors.Conflict("tax_duplicate", "a default rate already exists")
			}
		}
	}
	rate.CreatedAt = now.UTC()
	rate.UpdatedAt = now.UTC()
	m.rates[rate.ID] = rate
	return rate, nil
}

// GetTaxRate returns the live rate by id.
func (m *memRepo) GetTaxRate(_ context.Context, id string) (models.TaxRate, error) {
	rate, ok := m.rates[id]
	if !ok || rate.DeletedAt != nil {
		return models.TaxRate{}, errors.NotFound("tax_rate_not_found", "tax rate not found: %s", id)
	}
	return rate, nil
}

// ListTaxRates returns a region's live rates.
func (m *memRepo) ListTaxRates(_ context.Context, regionID string) ([]models.TaxRate, error) {
	return m.ratesFor([]string{regionID}), nil
}

// ListTaxRatesByRegions returns the live rates of several regions.
func (m *memRepo) ListTaxRatesByRegions(_ context.Context, regionIDs []string) ([]models.TaxRate, error) {
	return m.ratesFor(regionIDs), nil
}

// ratesFor returns the rates of the given regions in order.
func (m *memRepo) ratesFor(regionIDs []string) []models.TaxRate {
	out := make([]models.TaxRate, 0, len(m.rates))
	// The loop runs over the keys: ranging over the values would copy the
	// whole model on every iteration.
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

// UpdateTaxRate applies the patch.
func (m *memRepo) UpdateTaxRate(_ context.Context, id string, patch models.TaxRatePatch, now time.Time) (models.TaxRate, error) {
	current, ok := m.rates[id]
	if !ok || current.DeletedAt != nil {
		return models.TaxRate{}, errors.NotFound("tax_rate_not_found", "tax rate not found: %s", id)
	}
	updated := current.Patched(patch)
	updated.UpdatedAt = now.UTC()
	m.rates[id] = updated
	return updated, nil
}

// ReviseTaxRate writes the name and the rate only while they are the ones
// read.
func (m *memRepo) ReviseTaxRate(
	_ context.Context, id string, read, next models.TaxRateTerms, now time.Time,
) (models.TaxRate, bool, error) {
	current, ok := m.rates[id]
	if !ok || current.DeletedAt != nil || current.Name != read.Name || current.RateBps != read.RateBps {
		return models.TaxRate{}, false, nil
	}
	current.Name, current.RateBps, current.UpdatedAt = next.Name, next.RateBps, now.UTC()
	m.rates[id] = current

	return current, true, nil
}

// DeleteTaxRate deletes the rate and its rules.
func (m *memRepo) DeleteTaxRate(_ context.Context, id string, now time.Time) error {
	rate, ok := m.rates[id]
	if !ok || rate.DeletedAt != nil {
		return errors.NotFound("tax_rate_not_found", "tax rate not found: %s", id)
	}
	deleted := now.UTC()
	rate.DeletedAt = &deleted
	m.rates[id] = rate
	// The loop runs over the keys: ranging over the values would copy the
	// whole model on every iteration.
	for ruleID := range m.rules {
		rule := m.rules[ruleID]
		if rule.DeletedAt == nil && rule.TaxRateID == id {
			rule.DeletedAt = &deleted
			m.rules[ruleID] = rule
		}
	}
	return nil
}

// CreateTaxRateRule writes the rule; it rejects adding a rule to a default
// rate or to one standing on another.
func (m *memRepo) CreateTaxRateRule(_ context.Context, rule models.TaxRateRule, now time.Time) (models.TaxRateRule, error) {
	rate, ok := m.rates[rule.TaxRateID]
	if !ok || rate.DeletedAt != nil {
		return models.TaxRateRule{}, errors.NotFound("tax_rate_not_found",
			"tax rate not found: %s", rule.TaxRateID)
	}
	if rate.IsDefault {
		return models.TaxRateRule{}, errors.Conflict("tax_constraint_violation",
			"a default rate cannot have rules: %s", rule.TaxRateID)
	}
	if rate.StacksOnID != nil {
		return models.TaxRateRule{}, errors.Conflict("tax_constraint_violation",
			"a rate standing on another cannot have rules: %s", rule.TaxRateID)
	}
	rule.CreatedAt = now.UTC()
	rule.UpdatedAt = now.UTC()
	m.rules[rule.ID] = rule
	return rule, nil
}

// GetTaxRateRule returns the live rule by id.
func (m *memRepo) GetTaxRateRule(_ context.Context, id string) (models.TaxRateRule, error) {
	rule, ok := m.rules[id]
	if !ok || rule.DeletedAt != nil {
		return models.TaxRateRule{}, errors.NotFound("tax_rate_rule_not_found",
			"tax rule not found: %s", id)
	}
	return rule, nil
}

// ListTaxRateRules returns a rate's live rules.
func (m *memRepo) ListTaxRateRules(_ context.Context, rateID string) ([]models.TaxRateRule, error) {
	return m.rulesFor([]string{rateID}), nil
}

// ListTaxRateRulesByRates returns the live rules of several rates.
func (m *memRepo) ListTaxRateRulesByRates(_ context.Context, rateIDs []string) ([]models.TaxRateRule, error) {
	return m.rulesFor(rateIDs), nil
}

// rulesFor returns the rules of the given rates in order.
func (m *memRepo) rulesFor(rateIDs []string) []models.TaxRateRule {
	out := make([]models.TaxRateRule, 0, len(m.rules))
	// The loop runs over the keys: ranging over the values would copy the
	// whole model on every iteration.
	for key := range m.rules {
		rule := m.rules[key]
		if rule.DeletedAt == nil && slices.Contains(rateIDs, rule.TaxRateID) {
			out = append(out, rule)
		}
	}
	slices.SortFunc(out, func(a, b models.TaxRateRule) int { return compare(a.ID, b.ID) })
	return out
}

// DeleteTaxRateRule deletes the rule.
func (m *memRepo) DeleteTaxRateRule(_ context.Context, id string, now time.Time) error {
	rule, ok := m.rules[id]
	if !ok || rule.DeletedAt != nil {
		return errors.NotFound("tax_rate_rule_not_found", "tax rule not found: %s", id)
	}
	deleted := now.UTC()
	rule.DeletedAt = &deleted
	m.rules[id] = rule
	return nil
}

// compare compares two strings lexically.
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

// --- tax class ---------------------------------------------------------------

// CreateTaxClass writes the class.
func (m *memRepo) CreateTaxClass(
	_ context.Context, class models.TaxClass, now time.Time,
) (models.TaxClass, error) {
	class.CreatedAt, class.UpdatedAt = now, now
	m.classes[class.ID] = class

	return class, nil
}

// GetTaxClass reads the class by id.
func (m *memRepo) GetTaxClass(_ context.Context, id string) (models.TaxClass, error) {
	class, ok := m.classes[id]
	if !ok {
		return models.TaxClass{}, errors.NotFound("tax_class_not_found",
			"tax class not found: %s", id)
	}

	return class, nil
}

// ListTaxClasses returns the live classes by name.
func (m *memRepo) ListTaxClasses(_ context.Context) ([]models.TaxClass, error) {
	out := make([]models.TaxClass, 0, len(m.classes))
	for id := range m.classes {
		out = append(out, m.classes[id])
	}
	slices.SortFunc(out, func(a, b models.TaxClass) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}

		return strings.Compare(a.ID, b.ID)
	})

	return out, nil
}

// DeleteTaxClass deletes a class that carries no products.
func (m *memRepo) DeleteTaxClass(_ context.Context, id string, _ time.Time) error {
	if _, ok := m.classes[id]; !ok {
		return errors.NotFound("tax_class_not_found", "tax class not found: %s", id)
	}
	for _, classID := range m.members {
		if classID == id {
			return errors.Conflict("tax_constraint_violation",
				"class %s still carries products", id)
		}
	}
	delete(m.classes, id)

	return nil
}

// SetTaxClassMember binds the product to the class; if it is in another class
// it MOVES it.
func (m *memRepo) SetTaxClassMember(
	_ context.Context, member models.TaxClassMember, now time.Time,
) (models.TaxClassMember, error) {
	if _, ok := m.classes[member.TaxClassID]; !ok {
		return models.TaxClassMember{}, errors.NotFound("tax_class_not_found",
			"tax class not found: %s", member.TaxClassID)
	}
	m.members[member.ProductID] = member.TaxClassID
	member.CreatedAt, member.UpdatedAt = now, now

	return member, nil
}

// RemoveTaxClassMember takes the product out of its class.
func (m *memRepo) RemoveTaxClassMember(_ context.Context, productID string, _ time.Time) error {
	if _, ok := m.members[productID]; !ok {
		return errors.NotFound("tax_class_not_found",
			"the product is in no tax class: %s", productID)
	}
	delete(m.members, productID)

	return nil
}

// ListTaxClassMembers returns the class's products.
func (m *memRepo) ListTaxClassMembers(
	_ context.Context, classID string,
) ([]models.TaxClassMember, error) {
	out := make([]models.TaxClassMember, 0)
	for productID, bound := range m.members {
		if bound == classID {
			out = append(out, models.TaxClassMember{TaxClassID: classID, ProductID: productID})
		}
	}
	slices.SortFunc(out, func(a, b models.TaxClassMember) int {
		return strings.Compare(a.ProductID, b.ProductID)
	})

	return out, nil
}

// ClassesOfProducts returns the products' classes in one call; a product with no
// class is absent from the map.
func (m *memRepo) ClassesOfProducts(
	_ context.Context, productIDs []string,
) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range productIDs {
		if classID, ok := m.members[id]; ok {
			out[id] = classID
		}
	}

	return out, nil
}
