package service

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// Error codes of the tax rate trial (ADR 0387).
const (
	// CodeTrialInvalidPeriod reports a trial period that is missing, malformed
	// or reaches into the future.
	CodeTrialInvalidPeriod = "tax_trial_invalid_period"
	// CodeTrialInvalidChange reports a change that cannot be tried: none at
	// all, a malformed rule, a rule on shipping, a rule named twice, a rule to
	// drop the rate does not carry, or one to add it already carries.
	CodeTrialInvalidChange = "tax_trial_invalid_change"
	// CodeTrialChangeRefused reports a rule change on a rate a rule write
	// refuses one on: a default rate or a stacked one.
	CodeTrialChangeRefused = "tax_trial_change_refused"
	// CodeTrialRateUnreached reports a rate no cart reaches today: a
	// province's, since the cart sends no province.
	CodeTrialRateUnreached = "tax_trial_rate_unreached"
	// CodeTrialProviderExternal reports a rate in a country whose tax an
	// external provider computes; its table cannot be amended here.
	CodeTrialProviderExternal = "tax_trial_provider_external"
	// CodeTrialUnavailable reports that the flow the trial runs on is not
	// bound.
	CodeTrialUnavailable = "tax_trial_unavailable"
)

// The rate trial's bounds.
const (
	// MaxCompareEntries is the most orders one comparison taxes; it is the
	// cart flows' bound on a trial, repeated because neither side can import
	// the other.
	MaxCompareEntries = 5_000
	// MaxCompareItems is the most lines one comparison taxes.
	MaxCompareItems = 100_000
	// MaxTrialRuleChanges is the most rules one trial adds and drops together.
	MaxTrialRuleChanges = 100
)

// rateTrialAssumptions are what this module's side of a rate trial sets aside,
// published beside the figures: every other rate and rule, the tax classes and
// whether prices include their tax are read as they are today.
var rateTrialAssumptions = []string{"todays_rates", "todays_tax_classes", "todays_price_inclusion"}

// RuleKey is a rule a trial adds: what a rule write would be sent.
type RuleKey struct {
	Reference   string `json:"reference"`
	ReferenceID string `json:"reference_id"`
}

// RateChange is what a trial does to a rate: another value, rules added, rules
// dropped by id. Its JSON is what travels to the cart flows and back.
type RateChange struct {
	RateBps   *int32    `json:"rate_bps,omitempty"`
	AddRules  []RuleKey `json:"add_rules,omitempty"`
	DropRules []string  `json:"drop_rules,omitempty"`
}

// RateTrialFlow is the surface of the flow that taxes past orders' lines for a
// rate trial (ADR 0387).
//
// The question is about a tax rate and belongs here, but reading orders and
// rebuilding what a cart sent belong to internal/workflows/cart, which this
// module cannot import (ADR 0006). The flow asks back through
// [Interop.CompareRateJSON].
type RateTrialFlow interface {
	// TrialTaxRateJSON taxes the lines of the orders placed in [from, to) with
	// today's tables and with the change; it writes nothing.
	TrialTaxRateJSON(ctx context.Context, rateID string, from, to time.Time, change json.RawMessage) (json.RawMessage, error)
}

// CompareEntry is one order's lines as the cart sent them, for a comparison.
type CompareEntry struct {
	// Reference names the order; it comes back unchanged.
	Reference string
	// CountryCode is the country the cart sent.
	CountryCode string
	// Items are the lines sent, with the amount after discount.
	Items []TaxableItem
}

// CompareLine is one line's tax under one table.
type CompareLine struct {
	RateID    string `json:"rate_id"`
	TaxAmount int64  `json:"tax_amount"`
}

// CompareItem is one line taxed twice.
type CompareItem struct {
	ID string `json:"id"`
	// Reached says the rate under trial took part in either tax, as the rate
	// chosen or as a member of its stack.
	Reached  bool        `json:"reached"`
	Baseline CompareLine `json:"baseline"`
	Trial    CompareLine `json:"trial"`
}

// CompareAnswer is one order's lines taxed twice, in the request's order; an
// order outside the rate's country is marked and not taxed.
type CompareAnswer struct {
	Reference string        `json:"reference"`
	Outside   bool          `json:"outside"`
	Items     []CompareItem `json:"items"`
}

// rateTrialScope is a change checked against the rate's country: the two
// tables a line is taxed with and how.
type rateTrialScope struct {
	rateID   string
	country  string
	included bool
	baseline rateTable
	trial    rateTable
}

// TryRate checks a change to a rate and asks the flow to try it on the orders
// placed in [from, to); it writes nothing (ADR 0387).
//
// A change a write would refuse, a rate no cart reaches and a period reaching
// into the future are refused here, before the flow reads an order. The flow
// refuses a period that ends before it starts, is longer than its bound or
// holds more orders than its bound; more than [MaxCompareItems] lines is known
// only once the orders are read, and [Service.CompareRate] refuses it then.
func (s *Service) TryRate(
	ctx context.Context, flow RateTrialFlow, rateID string, from, to time.Time, change RateChange,
) (json.RawMessage, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if flow == nil {
		return nil, errors.Internal(CodeTrialUnavailable,
			"the tax rate trial flow is not bound; no order can be read")
	}
	if to.After(s.clock()) {
		return nil, errors.Invalid(CodeTrialInvalidPeriod,
			"a trial reads orders already placed; \"to\" is in the future: %s", to.Format(time.RFC3339))
	}
	if _, err := s.checkRateChange(ctx, rateID, change); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(change)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInteropResponseInvalid,
			"the change could not be encoded")
	}
	return flow.TrialTaxRateJSON(ctx, rateID, from, to, payload)
}

// CompareRate taxes the lines of past orders twice through the local rate
// table, as it is and with the change, and answers per order in the request's
// order beside what it assumed (ADR 0387).
//
// The change is checked again: the flow carries it, and this side does not
// take a change on trust because it was checked once.
func (s *Service) CompareRate(
	ctx context.Context, rateID string, change RateChange, entries []CompareEntry,
) ([]CompareAnswer, []string, error) {
	scope, err := s.checkRateChange(ctx, rateID, change)
	if err != nil {
		return nil, nil, err
	}
	if len(entries) > MaxCompareEntries {
		return nil, nil, errors.Invalid(CodeInvalidInput,
			"a comparison taxes at most %d orders, %d were sent", MaxCompareEntries, len(entries))
	}

	answers := make([]CompareAnswer, len(entries))
	var inside []TaxableItem
	for i := range entries {
		entry := &entries[i]
		if entry.Reference == "" {
			return nil, nil, errors.Invalid(CodeInvalidInput, "order %d of the comparison has no reference", i)
		}
		country, err := NormalizeCountryCode(entry.CountryCode)
		if err != nil {
			return nil, nil, err
		}
		answers[i] = CompareAnswer{Reference: entry.Reference, Items: []CompareItem{}}
		if country != scope.country {
			answers[i].Outside = true

			continue
		}
		seen := make(map[string]bool, len(entry.Items))
		for j := range entry.Items {
			item := entry.Items[j]
			if item.ID == "" || seen[item.ID] {
				return nil, nil, errors.Invalid(CodeInvalidInput,
					"line %d of order %s has an empty or repeated id: %q", j, entry.Reference, item.ID)
			}
			seen[item.ID] = true
			if err := checkTaxableAmount("the line's tax base", item.Amount); err != nil {
				return nil, nil, err
			}
			item.TaxClassID = ""
			inside = append(inside, item)
			if len(inside) > MaxCompareItems {
				return nil, nil, errors.Invalid(CodeInvalidInput,
					"a comparison taxes at most %d lines", MaxCompareItems)
			}
		}
	}

	if err := s.attachTaxClasses(ctx, inside); err != nil {
		return nil, nil, err
	}

	next := 0
	for i := range entries {
		if answers[i].Outside {
			continue
		}
		for range entries[i].Items {
			item, err := scope.compare(inside[next])
			if err != nil {
				return nil, nil, err
			}
			next++
			answers[i].Items = append(answers[i].Items, item)
		}
	}

	return answers, slices.Clone(rateTrialAssumptions), nil
}

// compare taxes one line with both tables.
func (sc rateTrialScope) compare(item TaxableItem) (CompareItem, error) {
	baseline, err := sc.tax(sc.baseline, item)
	if err != nil {
		return CompareItem{}, err
	}
	trial, err := sc.tax(sc.trial, item)
	if err != nil {
		return CompareItem{}, err
	}

	return CompareItem{
		ID:       item.ID,
		Reached:  reaches(baseline, sc.rateID) || reaches(trial, sc.rateID),
		Baseline: CompareLine{RateID: baseline.RateID, TaxAmount: baseline.TaxAmount},
		Trial:    CompareLine{RateID: trial.RateID, TaxAmount: trial.TaxAmount},
	}, nil
}

// tax applies a table to a line and holds the answer to the contract
// [Service.CalculateTax] holds a provider's to, so a figure the cart would
// refuse is refused here too.
func (sc rateTrialScope) tax(table rateTable, item TaxableItem) (ProviderItemTax, error) {
	line, err := table.applyTo(itemKeys(item), item.ID, item.Amount, sc.included)
	if err != nil {
		return ProviderItemTax{}, err
	}
	if _, err := validateLine(LocalProviderID, line, item.ID, item.Amount, sc.included); err != nil {
		return ProviderItemTax{}, err
	}

	return line, nil
}

// reaches says the rate took part in a line's tax: chosen, or in its stack.
func reaches(line ProviderItemTax, rateID string) bool {
	if line.RateID == rateID {
		return true
	}
	for i := range line.Components {
		if line.Components[i].RateID == rateID {
			return true
		}
	}

	return false
}

// checkRateChange refuses a change the write path would refuse and a rate no
// cart reaches, then builds the two tables a line is taxed with.
//
// The order is fixed: what needs no read first, then the rate, its region, its
// country's provider, and last the rows of the table.
func (s *Service) checkRateChange(ctx context.Context, rateID string, change RateChange) (rateTrialScope, error) {
	if err := s.ready(); err != nil {
		return rateTrialScope{}, err
	}
	if err := requireID(rateID, models.TaxRateIDPrefix, "tax rate id"); err != nil {
		return rateTrialScope{}, err
	}
	if err := checkChangeShape(change); err != nil {
		return rateTrialScope{}, err
	}

	rate, err := s.repo.GetTaxRate(ctx, rateID)
	if err != nil {
		return rateTrialScope{}, err
	}
	region, err := s.repo.GetTaxRegion(ctx, rate.TaxRegionID)
	if err != nil {
		return rateTrialScope{}, err
	}
	if !region.IsRoot() {
		return rateTrialScope{}, errors.Conflict(CodeTrialRateUnreached,
			"rate %s is province %s's; a cart sends no province, so no line it taxes reaches the rate",
			rateID, region.Province())
	}

	chain, err := s.repo.ResolveTaxRegions(ctx, region.CountryCode, "")
	if err != nil {
		return rateTrialScope{}, err
	}
	provider, err := s.providerFor(chain)
	if err != nil {
		return rateTrialScope{}, err
	}
	if provider.ID() != LocalProviderID {
		return rateTrialScope{}, errors.Conflict(CodeTrialProviderExternal,
			"the tax of %s is computed by the %q provider, whose table cannot be amended here",
			region.CountryCode, provider.ID())
	}

	regionIDs := make([]string, 0, len(chain))
	for i := range chain {
		regionIDs = append(regionIDs, chain[i].ID)
	}
	rates, rules, err := loadRateRows(ctx, s.repo, regionIDs)
	if err != nil {
		return rateTrialScope{}, err
	}

	trialRules, err := amendRules(rate, rules, change)
	if err != nil {
		return rateTrialScope{}, err
	}
	value := rate.RateBps
	if change.RateBps != nil {
		value = *change.RateBps
	}
	trialRates := slices.Clone(rates)
	for i := range trialRates {
		if trialRates[i].ID == rateID {
			trialRates[i].RateBps = value
		}
	}

	stack, err := stackHolding(rates, rateID, value)
	if err != nil {
		return rateTrialScope{}, err
	}
	if len(stack) > 1 {
		if err := assertStackWithinBase(stack); err != nil {
			return rateTrialScope{}, err
		}
	}

	return rateTrialScope{
		rateID:   rateID,
		country:  region.CountryCode,
		included: pricesIncludeTax(chain),
		baseline: newRateTable(regionIDs, rates, rules),
		trial:    newRateTable(regionIDs, trialRates, trialRules),
	}, nil
}

// checkChangeShape refuses what is wrong with a change before anything is
// read: nothing to try, too many rules, a rate outside the contract, a rule a
// write would refuse, and a rule named twice.
func checkChangeShape(change RateChange) error {
	ruleChanges := len(change.AddRules) + len(change.DropRules)
	if change.RateBps == nil && ruleChanges == 0 {
		return errors.Invalid(CodeTrialInvalidChange,
			"a trial needs a change: a rate, a rule to add or a rule to drop")
	}
	if ruleChanges > MaxTrialRuleChanges {
		return errors.Invalid(CodeTrialInvalidChange,
			"a trial adds and drops at most %d rules, %d were given", MaxTrialRuleChanges, ruleChanges)
	}
	if change.RateBps != nil {
		if err := validateRateBps(*change.RateBps); err != nil {
			return err
		}
	}

	added := make(map[RuleKey]bool, len(change.AddRules))
	for _, key := range change.AddRules {
		reference := models.RuleReference(key.Reference)
		if !reference.Valid() {
			return errors.Invalid(CodeTrialInvalidChange,
				"a rule's reference has to be %q, %q or %q; %q was given",
				models.ReferenceProduct, models.ReferenceTaxClass, models.ReferenceProductType, key.Reference)
		}
		if reference == models.ReferenceShippingOption {
			return errors.Invalid(CodeTrialInvalidChange,
				"a cart never taxes shipping, so a rule on a shipping option changes no line")
		}
		if err := requireReferenceID(key.ReferenceID); err != nil {
			return errors.Wrap(err, errors.KindInvalid, CodeTrialInvalidChange, "the rule %s cannot be tried", key.Reference)
		}
		if added[key] {
			return errors.Invalid(CodeTrialInvalidChange, "the rule %s:%s is added twice", key.Reference, key.ReferenceID)
		}
		added[key] = true
	}

	dropped := make(map[string]bool, len(change.DropRules))
	for _, id := range change.DropRules {
		if err := requireID(id, models.TaxRateRuleIDPrefix, "tax rate rule id"); err != nil {
			return errors.Wrap(err, errors.KindInvalid, CodeTrialInvalidChange, "a rule to drop cannot be tried")
		}
		if dropped[id] {
			return errors.Invalid(CodeTrialInvalidChange, "the rule %s is dropped twice", id)
		}
		dropped[id] = true
	}

	return nil
}

// amendRules returns the rules of the trial's table: the rate's own without
// the ones dropped, with the ones added, every other rate's as they are.
//
// A rule change is refused on the rates a rule write refuses one on, and an
// added rule is a row the write would make: the unique index allows one live
// rule per key on a rate, so adding one the rate carries is refused, and adding
// back one being dropped changes nothing.
func amendRules(rate models.TaxRate, rules []models.TaxRateRule, change RateChange) ([]models.TaxRateRule, error) {
	if len(change.AddRules)+len(change.DropRules) == 0 {
		return rules, nil
	}
	if rate.IsDefault || rate.StacksOnID != nil {
		return nil, errors.Conflict(CodeTrialChangeRefused,
			"rate %s is a default or stands on another rate, and a rule write refuses a rule on it", rate.ID)
	}

	own := map[RuleKey]string{}
	carries := map[string]bool{}
	for i := range rules {
		if rules[i].TaxRateID == rate.ID {
			own[RuleKey{Reference: rules[i].Reference.String(), ReferenceID: rules[i].ReferenceID}] = rules[i].ID
			carries[rules[i].ID] = true
		}
	}
	dropped := make(map[string]bool, len(change.DropRules))
	for _, id := range change.DropRules {
		if !carries[id] {
			return nil, errors.Invalid(CodeTrialInvalidChange, "rate %s carries no rule %s", rate.ID, id)
		}
		dropped[id] = true
	}
	for _, key := range change.AddRules {
		id, carried := own[key]
		switch {
		case carried && dropped[id]:
			return nil, errors.Invalid(CodeTrialInvalidChange,
				"the rule %s:%s is dropped and added back, which changes nothing", key.Reference, key.ReferenceID)
		case carried:
			return nil, errors.Invalid(CodeTrialInvalidChange,
				"rate %s already carries the rule %s:%s", rate.ID, key.Reference, key.ReferenceID)
		}
	}

	out := make([]models.TaxRateRule, 0, len(rules)+len(change.AddRules))
	for i := range rules {
		if !dropped[rules[i].ID] {
			out = append(out, rules[i])
		}
	}
	for _, key := range change.AddRules {
		out = append(out, models.TaxRateRule{
			TaxRateID: rate.ID, Reference: models.RuleReference(key.Reference), ReferenceID: key.ReferenceID,
		})
	}

	return out, nil
}
