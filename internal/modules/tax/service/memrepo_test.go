package service

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// memRepo is an in-memory implementation of [Repository].
//
// Its purpose is to be able to verify the service's RULES without a database:
// id generation, normalization, rate selection, rounding, error classification
// and the NUMBER OF QUERIES made. Database-specific claims (the partial unique
// index refusing a second root region, the composite foreign key preventing a
// province-country mismatch, the row lock separating two concurrent updates)
// are proven NOT HERE but in the integration tests — a fake repository cannot
// verify a rule it wrote itself.
//
// The in-memory counterparts of the same constraints are applied all the same:
// the service branches on the CLASS of those errors, and had the fake
// repository never produced them, the service's conflict paths would never be
// tested.
//
// Repository calls are counted (calls): that is the proof of the claim "no
// query is made per line item".
type memRepo struct {
	mu sync.Mutex

	regions map[string]models.TaxRegion
	rates   map[string]models.TaxRate
	rules   map[string]models.TaxRateRule
	classes map[string]models.TaxClass
	// members binds a product id to a class id; the same as
	// tax_class_member_product_uniq in the real schema, so a product is in AT
	// MOST one class.
	members map[string]string

	// calls is the call counter by method name.
	calls map[string]int
	// failOn holds, for a method name that is set, the error that call returns.
	failOn map[string]error
}

var _ Repository = (*memRepo)(nil)
var _ RateSource = (*memRepo)(nil)

// newMemRepo builds an empty in-memory repository.
func newMemRepo() *memRepo {
	return &memRepo{
		regions: map[string]models.TaxRegion{},
		rates:   map[string]models.TaxRate{},
		rules:   map[string]models.TaxRateRule{},
		classes: map[string]models.TaxClass{},
		members: map[string]string{},
		calls:   map[string]int{},
		failOn:  map[string]error{},
	}
}

// enter counts the call and returns the scripted error.
func (m *memRepo) enter(name string) error {
	m.calls[name]++
	if err, ok := m.failOn[name]; ok {
		return err
	}
	return nil
}

// WithTx runs fn AS IS; it does NOT ROLL BACK.
//
// This is where the fake is deliberately incomplete, and the gap has to be
// written down: an in-memory repository cannot open a transaction, so this
// method passing does NOT mean "the write was atomic". The only thing proven
// here is that the service CALLS the transaction frame (visible through the
// call counter). That the frame really rolls back and the lock really makes
// others wait can be shown only in the integration tests, on real row locks —
// a fake repository cannot verify a rule it wrote itself.
//
// The rollback was NOT imitated; had it been, it would be even worse: a write
// convincingly rolled back in memory would hide a write the database does not
// roll back, and the tests would stay green at exactly that moment.
func (m *memRepo) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	m.mu.Lock()
	err := m.enter("WithTx")
	m.mu.Unlock()
	if err != nil {
		return err
	}
	return fn(ctx)
}

// LockTaxRegion returns the live region; there is NO lock (see
// [memRepo.WithTx]).
func (m *memRepo) LockTaxRegion(_ context.Context, id string) (models.TaxRegion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("LockTaxRegion"); err != nil {
		return models.TaxRegion{}, err
	}

	region, ok := m.regions[id]
	if !ok || region.DeletedAt != nil {
		return models.TaxRegion{}, errors.NotFound("tax_region_not_found",
			"tax region not found: %s", id)
	}
	return region, nil
}

// CreateTaxRegion writes the region; it rejects a country's second root.
func (m *memRepo) CreateTaxRegion(_ context.Context, region models.TaxRegion, now time.Time) (models.TaxRegion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("CreateTaxRegion"); err != nil {
		return models.TaxRegion{}, err
	}

	// The loop runs over the keys: ranging over the values would copy the
	// whole model on every iteration.
	for key := range m.regions {
		existing := m.regions[key]
		if existing.DeletedAt != nil || existing.CountryCode != region.CountryCode {
			continue
		}
		if region.IsRoot() && existing.IsRoot() {
			// The code is the SAME as the one the real repository produces by
			// constraint name (repository.duplicateCode); had the fake returned
			// the general "tax_duplicate", the error the service sees would
			// differ from the real one.
			return models.TaxRegion{}, errors.Conflict(CodeRootExists,
				"a root region already exists (constraint: tax_region_country_root_uniq)")
		}
		if !region.IsRoot() && !existing.IsRoot() &&
			existing.Parent() == region.Parent() && existing.Province() == region.Province() {
			return models.TaxRegion{}, errors.Conflict("tax_duplicate",
				"a province region already exists (constraint: tax_region_province_uniq)")
		}
	}
	if !region.IsRoot() {
		parent, ok := m.regions[region.Parent()]
		if !ok || parent.DeletedAt != nil || parent.CountryCode != region.CountryCode {
			return models.TaxRegion{}, errors.Invalid("tax_constraint_violation",
				"the parent was not found (constraint: tax_region_parent_fk)")
		}
	}

	region.CreatedAt = now.UTC()
	region.UpdatedAt = now.UTC()
	m.regions[region.ID] = region
	return region, nil
}

// GetTaxRegion returns the live region by id.
func (m *memRepo) GetTaxRegion(_ context.Context, id string) (models.TaxRegion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("GetTaxRegion"); err != nil {
		return models.TaxRegion{}, err
	}

	region, ok := m.regions[id]
	if !ok || region.DeletedAt != nil {
		return models.TaxRegion{}, errors.NotFound("tax_region_not_found",
			"tax region not found: %s", id)
	}
	return region, nil
}

// GetTaxRegionsByIDs returns the live regions of the given ids.
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

// ListTaxRegions returns the paged list of regions.
func (m *memRepo) ListTaxRegions(_ context.Context, countryCode string, limit, offset int32) ([]models.TaxRegion, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ListTaxRegions"); err != nil {
		return nil, 0, err
	}

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
	sortRegions(all)

	total := int64(len(all))
	if int(offset) >= len(all) {
		return []models.TaxRegion{}, total, nil
	}
	end := min(int(offset)+int(limit), len(all))
	return slices.Clone(all[offset:end]), total, nil
}

// ResolveTaxRegions returns the country's root and (if given) its province;
// the province comes FIRST.
func (m *memRepo) ResolveTaxRegions(_ context.Context, countryCode, provinceCode string) ([]models.TaxRegion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ResolveTaxRegions"); err != nil {
		return nil, err
	}

	var root, province []models.TaxRegion
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
	sortRegions(root)
	sortRegions(province)
	return append(province, root...), nil
}

// DeleteTaxRegion deletes the region, its child regions, their rates and their
// rules.
func (m *memRepo) DeleteTaxRegion(_ context.Context, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("DeleteTaxRegion"); err != nil {
		return err
	}

	region, ok := m.regions[id]
	if !ok || region.DeletedAt != nil {
		return errors.NotFound("tax_region_not_found", "tax region not found: %s", id)
	}

	deleted := now.UTC()
	regionIDs := map[string]bool{}
	// The loop runs over the keys: ranging over the values would copy the
	// whole model on every iteration.
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
	// The loop runs over the keys: ranging over the values would copy the
	// whole model on every iteration.
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
	// The loop runs over the keys: ranging over the values would copy the
	// whole model on every iteration.
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

// CreateTaxRate writes the rate; it rejects a region's second default.
func (m *memRepo) CreateTaxRate(_ context.Context, rate models.TaxRate, now time.Time) (models.TaxRate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("CreateTaxRate"); err != nil {
		return models.TaxRate{}, err
	}

	region, ok := m.regions[rate.TaxRegionID]
	if !ok || region.DeletedAt != nil {
		return models.TaxRate{}, errors.Invalid("tax_constraint_violation",
			"the region was not found (constraint: tax_rate_region_fk)")
	}
	if rate.IsDefault && m.defaultRateLocked(rate.TaxRegionID) != "" {
		// The code is the same as the real repository's; see CreateTaxRegion.
		return models.TaxRate{}, errors.Conflict(CodeDefaultExists,
			"a default rate already exists (constraint: tax_rate_default_uniq)")
	}

	rate.CreatedAt = now.UTC()
	rate.UpdatedAt = now.UTC()
	m.rates[rate.ID] = rate
	return rate, nil
}

// defaultRateLocked returns the id of the region's default rate; the caller
// has to hold the lock.
func (m *memRepo) defaultRateLocked(regionID string) string {
	// The loop runs over the keys: ranging over the values would copy the
	// whole model on every iteration.
	for id := range m.rates {
		rate := m.rates[id]
		if rate.DeletedAt == nil && rate.TaxRegionID == regionID && rate.IsDefault {
			return id
		}
	}
	return ""
}

// GetTaxRate returns the live rate by id.
func (m *memRepo) GetTaxRate(_ context.Context, id string) (models.TaxRate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("GetTaxRate"); err != nil {
		return models.TaxRate{}, err
	}

	rate, ok := m.rates[id]
	if !ok || rate.DeletedAt != nil {
		return models.TaxRate{}, errors.NotFound("tax_rate_not_found", "tax rate not found: %s", id)
	}
	return rate, nil
}

// ListTaxRates returns a region's live rates.
func (m *memRepo) ListTaxRates(_ context.Context, regionID string) ([]models.TaxRate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ListTaxRates"); err != nil {
		return nil, err
	}
	return m.ratesForLocked([]string{regionID}), nil
}

// ListTaxRatesByRegions returns the live rates of several regions.
func (m *memRepo) ListTaxRatesByRegions(_ context.Context, regionIDs []string) ([]models.TaxRate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ListTaxRatesByRegions"); err != nil {
		return nil, err
	}
	return m.ratesForLocked(regionIDs), nil
}

// ratesForLocked returns the live rates of the given regions in order; the
// caller has to hold the lock.
func (m *memRepo) ratesForLocked(regionIDs []string) []models.TaxRate {
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

// ReviseTaxRate writes the name and the rate only while they are the ones
// read, as the statement's WHERE does.
func (m *memRepo) ReviseTaxRate(
	_ context.Context, id string, read, next models.TaxRateTerms, now time.Time,
) (models.TaxRate, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ReviseTaxRate"); err != nil {
		return models.TaxRate{}, false, err
	}

	current, ok := m.rates[id]
	if !ok || current.DeletedAt != nil || current.Name != read.Name || current.RateBps != read.RateBps {
		return models.TaxRate{}, false, nil
	}
	current.Name, current.RateBps, current.UpdatedAt = next.Name, next.RateBps, now.UTC()
	m.rates[id] = current

	return current, true, nil
}

// UpdateTaxRate applies the patch; it checks the default-rate constraints.
func (m *memRepo) UpdateTaxRate(_ context.Context, id string, patch models.TaxRatePatch, now time.Time) (models.TaxRate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("UpdateTaxRate"); err != nil {
		return models.TaxRate{}, err
	}

	current, ok := m.rates[id]
	if !ok || current.DeletedAt != nil {
		return models.TaxRate{}, errors.NotFound("tax_rate_not_found", "tax rate not found: %s", id)
	}

	updated := current.Patched(patch)
	if updated.IsDefault && !current.IsDefault {
		if m.ruleCountLocked(id) > 0 {
			return models.TaxRate{}, errors.Conflict("tax_constraint_violation",
				"a ruled rate cannot be made the default: %s", id)
		}
		if other := m.defaultRateLocked(current.TaxRegionID); other != "" && other != id {
			return models.TaxRate{}, errors.Conflict(CodeDefaultExists,
				"a default rate already exists (constraint: tax_rate_default_uniq)")
		}
	}

	updated.UpdatedAt = now.UTC()
	m.rates[id] = updated
	return updated, nil
}

// ruleCountLocked returns the number of the rate's live rules; the caller has
// to hold the lock.
func (m *memRepo) ruleCountLocked(rateID string) int {
	count := 0
	// The loop runs over the keys: ranging over the values would copy the
	// whole model on every iteration.
	for key := range m.rules {
		rule := m.rules[key]
		if rule.DeletedAt == nil && rule.TaxRateID == rateID {
			count++
		}
	}
	return count
}

// DeleteTaxRate deletes the rate and its rules.
func (m *memRepo) DeleteTaxRate(_ context.Context, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("DeleteTaxRate"); err != nil {
		return err
	}

	rate, ok := m.rates[id]
	if !ok || rate.DeletedAt != nil {
		return errors.NotFound("tax_rate_not_found", "tax rate not found: %s", id)
	}

	deleted := now.UTC()
	rate.DeletedAt = &deleted
	rate.UpdatedAt = deleted
	m.rates[id] = rate

	// The loop runs over the keys: ranging over the values would copy the
	// whole model on every iteration.
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

// CreateTaxRateRule writes the rule; it rejects adding a rule to a default
// rate.
func (m *memRepo) CreateTaxRateRule(_ context.Context, rule models.TaxRateRule, now time.Time) (models.TaxRateRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("CreateTaxRateRule"); err != nil {
		return models.TaxRateRule{}, err
	}

	rate, ok := m.rates[rule.TaxRateID]
	if !ok || rate.DeletedAt != nil {
		return models.TaxRateRule{}, errors.NotFound("tax_rate_not_found",
			"tax rate not found: %s", rule.TaxRateID)
	}
	if rate.IsDefault {
		return models.TaxRateRule{}, errors.Conflict("tax_constraint_violation",
			"a default rate cannot have rules: %s", rule.TaxRateID)
	}
	// The loop runs over the keys: ranging over the values would copy the
	// whole model on every iteration.
	for key := range m.rules {
		existing := m.rules[key]
		if existing.DeletedAt == nil && existing.TaxRateID == rule.TaxRateID &&
			existing.Reference == rule.Reference && existing.ReferenceID == rule.ReferenceID {
			return models.TaxRateRule{}, errors.Conflict("tax_duplicate",
				"the rule already exists (constraint: tax_rate_rule_uniq)")
		}
	}

	rule.CreatedAt = now.UTC()
	rule.UpdatedAt = now.UTC()
	m.rules[rule.ID] = rule
	return rule, nil
}

// GetTaxRateRule returns the live rule by id.
func (m *memRepo) GetTaxRateRule(_ context.Context, id string) (models.TaxRateRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("GetTaxRateRule"); err != nil {
		return models.TaxRateRule{}, err
	}

	rule, ok := m.rules[id]
	if !ok || rule.DeletedAt != nil {
		return models.TaxRateRule{}, errors.NotFound("tax_rate_rule_not_found",
			"tax rule not found: %s", id)
	}
	return rule, nil
}

// ListTaxRateRules returns a rate's live rules.
func (m *memRepo) ListTaxRateRules(_ context.Context, rateID string) ([]models.TaxRateRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ListTaxRateRules"); err != nil {
		return nil, err
	}
	return m.rulesForLocked([]string{rateID}), nil
}

// ListTaxRateRulesByRates returns the live rules of several rates.
func (m *memRepo) ListTaxRateRulesByRates(_ context.Context, rateIDs []string) ([]models.TaxRateRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ListTaxRateRulesByRates"); err != nil {
		return nil, err
	}
	return m.rulesForLocked(rateIDs), nil
}

// rulesForLocked returns the live rules of the given rates in order.
func (m *memRepo) rulesForLocked(rateIDs []string) []models.TaxRateRule {
	out := make([]models.TaxRateRule, 0, len(m.rules))
	// The loop runs over the keys: ranging over the values would copy the
	// whole model on every iteration.
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

// DeleteTaxRateRule deletes the rule.
func (m *memRepo) DeleteTaxRateRule(_ context.Context, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("DeleteTaxRateRule"); err != nil {
		return err
	}

	rule, ok := m.rules[id]
	if !ok || rule.DeletedAt != nil {
		return errors.NotFound("tax_rate_rule_not_found", "tax rule not found: %s", id)
	}

	deleted := now.UTC()
	rule.DeletedAt = &deleted
	rule.UpdatedAt = deleted
	m.rules[id] = rule
	return nil
}

// callCount returns the number of calls of a method.
func (m *memRepo) callCount(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[name]
}

// sortRegions puts the regions in a deterministic order: the root FIRST, then
// by id.
//
// It does not have to be the same as the order in the real query; what is
// required is that the fake repository does NOT CHANGE WITH the map iteration
// order. Otherwise the tests would be unstable among themselves.
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

// --- tax class ---------------------------------------------------------------

// CreateTaxClass writes the class.
func (m *memRepo) CreateTaxClass(
	_ context.Context, class models.TaxClass, now time.Time,
) (models.TaxClass, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("CreateTaxClass"); err != nil {
		return models.TaxClass{}, err
	}

	class.CreatedAt, class.UpdatedAt = now, now
	m.classes[class.ID] = class

	return class, nil
}

// GetTaxClass reads the class by id.
func (m *memRepo) GetTaxClass(_ context.Context, id string) (models.TaxClass, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("GetTaxClass"); err != nil {
		return models.TaxClass{}, err
	}

	class, ok := m.classes[id]
	if !ok {
		return models.TaxClass{}, errors.NotFound("tax_class_not_found",
			"tax class not found: %s", id)
	}

	return class, nil
}

// ListTaxClasses returns the live classes.
func (m *memRepo) ListTaxClasses(_ context.Context) ([]models.TaxClass, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ListTaxClasses"); err != nil {
		return nil, err
	}

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
//
// The number of products is checked here too, as in the REAL repository: a fake
// that did not ask for the count would make the service's "a class with
// products is not deleted" branch untestable.
func (m *memRepo) DeleteTaxClass(_ context.Context, id string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("DeleteTaxClass"); err != nil {
		return err
	}

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
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("SetTaxClassMember"); err != nil {
		return models.TaxClassMember{}, err
	}

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
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("RemoveTaxClassMember"); err != nil {
		return err
	}

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
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ListTaxClassMembers"); err != nil {
		return nil, err
	}

	out := make([]models.TaxClassMember, 0)
	for productID, bound := range m.members {
		if bound == classID {
			out = append(out, models.TaxClassMember{
				TaxClassID: classID, ProductID: productID,
			})
		}
	}
	slices.SortFunc(out, func(a, b models.TaxClassMember) int {
		return strings.Compare(a.ProductID, b.ProductID)
	})

	return out, nil
}

// ClassesOfProducts returns the products' classes in one call.
//
// A product with no class is ABSENT from the map — the real query returns no
// row for it either — because a fake that wrote a zero value would leave the
// caller's "no class" branch untestable.
func (m *memRepo) ClassesOfProducts(
	_ context.Context, productIDs []string,
) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ClassesOfProducts"); err != nil {
		return nil, err
	}

	out := map[string]string{}
	for _, id := range productIDs {
		if classID, ok := m.members[id]; ok {
			out[id] = classID
		}
	}

	return out, nil
}
