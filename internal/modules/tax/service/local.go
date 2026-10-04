package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/internal/modules/tax/models"

	"github.com/bdrtr/gobit/core/errors"
)

// LocalProvider computes tax from this module's OWN tables.
//
// It is the provider that ships in the box, and it is used when NO link in the
// region chain names a provider; an empty provider_id does not mean "local"
// but "my parent's provider" (see [Service.providerFor]).
// It never goes to an external tax service (Avalara, TaxJar …); it reads rates
// and rules through [RateSource].
//
// # Rate selection
//
// A SINGLE rate is SELECTED for a line item; if rates stand on it, the line is
// taxed by that whole stack and the components add (see [rateTable.stackFrom],
// ADR 0095). The selection walks the region chain from the most SPECIFIC to the
// general (province, then country), and in each region this order applies:
//
//  1. Among the ruled rates that MATCH the line item, the most SPECIFIC one
//     wins: a rule written for a single product beats a rule written for that
//     product's type (see [models.RuleReference.Specificity]).
//  2. If specificity is equal, the one with the SMALLER ID wins. Since ids are
//     time ordered, this means "the one defined first wins" and makes the
//     result DETERMINISTIC; without an ordering rule the same cart could
//     produce two different taxes on two calls.
//  3. If no rule matches, the region's DEFAULT rate applies.
//  4. If the region yields no rate at all (neither a matching rule nor a
//     default), the walk moves to the NEXT link UP the chain.
//
// # The province OVERRIDES the country, the chain's rates are NOT ADDED
//
// The decision is deliberate. Addition (province + country) is right only in
// jurisdictions where sub-national tax really is additive (US state + county
// sales tax). In KDV/VAT countries — Turkey, the EU — the country rate is the
// WHOLE tax, and the moment a province row was added every cart would be taxed
// twice. If an additive structure is needed, it is expressed as a STACK in the
// SAME region (e.g. a 2% county rate standing on a 6% state rate, ADR 0095);
// the selected rate expands into its stack and every component is carried on
// the line separately.
//
// # Why moving up the chain exists
//
// A province region is most often opened for A SINGLE EXCEPTION (e.g. a
// reduced rate for a product group). If no default rate was defined in that
// region, the expected behavior is for line items that match no rule to fall
// back to the country's rate instead of staying untaxed. On the other hand, if
// the province HAS a DEFAULT rate, the country's rate never shows: the province
// has given the full answer for that geography.
type LocalProvider struct {
	rates RateSource
}

// NewLocalProvider builds the local provider that runs on the given rate
// source.
func NewLocalProvider(rates RateSource) *LocalProvider {
	return &LocalProvider{rates: rates}
}

// ID returns the provider's id.
func (p *LocalProvider) ID() string { return LocalProviderID }

// Calculate computes the tax of the line items and (if asked for) of shipping.
//
// If the region chain is empty, no query is made and every tax comes back
// zero; a country whose tax is not configured produces zero, not an error
// (see [Service.CalculateTax]).
func (p *LocalProvider) Calculate(ctx context.Context, in ProviderInput) (ProviderResult, error) {
	out := ProviderResult{
		Items:    make([]ProviderItemTax, 0, len(in.Items)),
		Shipping: ProviderItemTax{ID: ShippingLineID},
	}
	if len(in.RegionIDs) == 0 || (len(in.Items) == 0 && !in.Shipping.Taxable) {
		for i := range in.Items {
			out.Items = append(out.Items,
				ProviderItemTax{ID: in.Items[i].ID, TaxableAmount: in.Items[i].Amount})
		}
		return out, nil
	}

	table, err := p.loadRates(ctx, in.RegionIDs)
	if err != nil {
		return ProviderResult{}, err
	}

	for i := range in.Items {
		tax, err := table.applyTo(
			itemKeys(in.Items[i]), in.Items[i].ID, in.Items[i].Amount, in.PricesIncludeTax)
		if err != nil {
			return ProviderResult{}, err
		}
		out.Items = append(out.Items, tax)
	}

	if in.Shipping.Taxable {
		tax, err := table.applyTo(
			shippingKeys(in.Shipping), ShippingLineID, in.Shipping.Amount, in.PricesIncludeTax)
		if err != nil {
			return ProviderResult{}, err
		}
		out.Shipping = tax
	}
	return out, nil
}

// loadRates reads the rates and rules of the region chain in TWO queries.
//
// The number of queries is INDEPENDENT of the number of line items and of the
// chain's length: one line item and a thousand make the same two round trips. A
// rate query per line item would turn the calculation into an N+1 that grows
// with the size of the cart.
func (p *LocalProvider) loadRates(ctx context.Context, regionIDs []string) (rateTable, error) {
	rates, rules, err := loadRateRows(ctx, p.rates, regionIDs)
	if err != nil {
		return rateTable{}, err
	}
	return newRateTable(regionIDs, rates, rules), nil
}

// loadRateRows reads the rows [newRateTable] is built from: the chain's rates
// and the rules of its ruled rates.
//
// It is the one read both the calculation and the rate trial build their table
// from (ADR 0387), so a trial's baseline cannot come to read other rows than
// the cart is charged from.
func loadRateRows(
	ctx context.Context, source RateSource, regionIDs []string,
) ([]models.TaxRate, []models.TaxRateRule, error) {
	rates, err := source.ListTaxRatesByRegions(ctx, regionIDs)
	if err != nil {
		return nil, nil, err
	}

	ruledIDs := make([]string, 0, len(rates))
	for i := range rates {
		if !rates[i].IsDefault {
			ruledIDs = append(ruledIDs, rates[i].ID)
		}
	}

	var rules []models.TaxRateRule
	if len(ruledIDs) > 0 {
		rules, err = source.ListTaxRateRulesByRates(ctx, ruledIDs)
		if err != nil {
			return nil, nil, err
		}
	}
	return rates, rules, nil
}

// matchKey is a single property of a line item to be matched against rules.
type matchKey struct {
	reference   models.RuleReference
	referenceID string
}

// itemKeys produces a line item's match keys.
//
// Empty ids are SKIPPED: an empty product type means "a product with no type",
// and a rule with an empty referenceID cannot be written anyway (CHECK
// constraint), so an empty key has no chance of matching — putting it on the
// list would only produce needless comparisons.
func itemKeys(item TaxableItem) []matchKey {
	keys := make([]matchKey, 0, 3)
	if item.ProductID != "" {
		keys = append(keys, matchKey{models.ReferenceProduct, item.ProductID})
	}
	if item.TaxClassID != "" {
		keys = append(keys, matchKey{models.ReferenceTaxClass, item.TaxClassID})
	}
	if item.ProductTypeID != "" {
		keys = append(keys, matchKey{models.ReferenceProductType, item.ProductTypeID})
	}
	return keys
}

// shippingKeys produces the shipping line's match keys.
func shippingKeys(shipping ShippingInput) []matchKey {
	if shipping.OptionID == "" {
		return nil
	}
	return []matchKey{{models.ReferenceShippingOption, shipping.OptionID}}
}

// ruledRate is a ruled rate together with its rules.
type ruledRate struct {
	rate  models.TaxRate
	rules []models.TaxRateRule
}

// rateTable is the region chain's rates prepared for the calculation.
//
// It is a PURE data structure: it touches no database, clock or logging. This
// separation is what lets the selection rule be tested on its own, without a
// real provider or pool.
type rateTable struct {
	// chain holds the region ids; it is ordered from the most SPECIFIC to the
	// general.
	chain []string
	// ruled maps a region id to that region's ruled rates; each slice is in
	// ASCENDING order of rate id.
	ruled map[string][]ruledRate
	// fallback maps a region id to that region's default rate.
	fallback map[string]models.TaxRate
	// standing maps a rate's id to the rate standing ON TOP OF IT.
	//
	// A stack is a LIST: at most one rate stands on each base (partial unique
	// index), so this map is enough to walk the stack in order, and no
	// position column is needed.
	standing map[string]models.TaxRate
}

// newRateTable turns rates and rules into the calculation table.
//
// The rules of default rates are DELIBERATELY ignored: the service and
// repository layers already refuse to write a rule onto a default rate, but a
// rule written by hand (directly with SQL) must not silently narrow the rate's
// scope here. A default rate is, by its name, the rate applied without looking
// for a match.
func newRateTable(chain []string, rates []models.TaxRate, rules []models.TaxRateRule) rateTable {
	byRate := make(map[string][]models.TaxRateRule, len(rules))
	for i := range rules {
		byRate[rules[i].TaxRateID] = append(byRate[rules[i].TaxRateID], rules[i])
	}

	table := rateTable{
		chain:    slices.Clone(chain),
		ruled:    make(map[string][]ruledRate, len(chain)),
		fallback: make(map[string]models.TaxRate, len(chain)),
		standing: make(map[string]models.TaxRate, len(rates)),
	}
	for i := range rates {
		rate := rates[i]
		if rate.StacksOnID != nil {
			// A rate standing ON TOP of another rate is never SELECTED: it is
			// reached by expanding the selected rate. Being the default and
			// being built on a ruled rate are refused at write time; dropping it
			// from the candidates here is that refusal's second line of
			// defense, just as the same row drops a default rate's hand-written
			// rules.
			//
			// At most one rate can stand on a base (partial unique index). If a
			// second one arrives anyway, the one with the SMALLER ID is kept;
			// silently taking the last one would tie the tax to row order.
			if existing, ok := table.standing[*rate.StacksOnID]; !ok || rate.ID < existing.ID {
				table.standing[*rate.StacksOnID] = rate
			}
			continue
		}
		if rate.IsDefault {
			// A region can have at most one default rate (partial unique
			// index). If a second one arrives anyway, the one with the SMALLER
			// ID is kept; silently taking the last one would tie the result to
			// row order.
			if existing, ok := table.fallback[rate.TaxRegionID]; !ok || rate.ID < existing.ID {
				table.fallback[rate.TaxRegionID] = rate
			}
			continue
		}
		table.ruled[rate.TaxRegionID] = append(table.ruled[rate.TaxRegionID], ruledRate{
			rate:  rate,
			rules: byRate[rate.ID],
		})
	}

	for regionID := range table.ruled {
		slices.SortFunc(table.ruled[regionID], func(a, b ruledRate) int {
			return compareStrings(a.rate.ID, b.rate.ID)
		})
	}
	return table
}

// compareStrings compares two strings lexically.
//
// Using a small helper instead of strings.Compare keeps the sort criterion (id
// ascending) readable at the call site.
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

// selectRate selects the rate to apply for the given keys.
//
// The order and its reasons are in the [LocalProvider] godoc. If no rate is
// found, the second return value is false and the tax is zero.
func (t rateTable) selectRate(keys []matchKey) (models.TaxRate, bool) {
	for _, regionID := range t.chain {
		best, bestSpec, found := models.TaxRate{}, 0, false
		candidates := t.ruled[regionID]
		for i := range candidates {
			spec := matchSpecificity(candidates[i].rules, keys)
			// Using strict greater-than (>) keeps the FIRST candidate on a tie;
			// since the slice is sorted by id, that means "the smallest id
			// wins".
			if spec > bestSpec {
				best, bestSpec, found = candidates[i].rate, spec, true
			}
		}
		if found {
			return best, true
		}
		if fallback, ok := t.fallback[regionID]; ok {
			return fallback, true
		}
	}
	return models.TaxRate{}, false
}

// applyTo applies the selected rate to the given amount.
//
// There are two calculations and `included` decides which one runs. In a
// market that is not inclusive the amount is NET, the tax is added on top, and
// the base is the amount itself. In an inclusive market the amount is GROSS,
// the tax is extracted from inside it, and the base is what remains — so that
// adding the base and the tax gives the figure the customer saw. Neither can
// stand in for the other; the reasoning and the measurement are in
// [TaxIncludedIn].
func (t rateTable) applyTo(
	keys []matchKey, lineID string, amount int64, included bool,
) (ProviderItemTax, error) {
	rate, ok := t.selectRate(keys)
	if !ok {
		// When no rate is found the tax is zero and the base is the amount
		// itself — in an inclusive market too, because there is no tax to
		// extract from inside it.
		return ProviderItemTax{ID: lineID, TaxableAmount: amount}, nil
	}

	stack, err := t.stackFrom(rate)
	if err != nil {
		return ProviderItemTax{}, err
	}

	if included {
		// INCLUSIVE PRICE + STACK IS REFUSED. TaxIncludedIn's divisor is
		// 10000+rate for a SINGLE rate (money.go); n components would need a
		// reverse peel that loads the rounding remainder onto one component BY
		// DECREE, and ADR 0086's measurement is the record of how easily that
		// goes wrong.
		//
		// It is refused at write time too (in the rate write and in the region
		// write), so a configuration that lands here means one of those gates
		// was breached: instead of silently producing a wrong number, it stops.
		if len(stack) > 1 {
			return ProviderItemTax{}, errors.Internal(CodeInconsistentConfig,
				"in a region whose prices include tax, rate %s starts a STACK "+
					"(%d components); on an inclusive price the reverse calculation is defined for a single rate",
				rate.ID, len(stack))
		}

		tax, taxErr := TaxIncludedIn(amount, rate.RateBps)
		if taxErr != nil {
			return ProviderItemTax{}, taxErr
		}

		return ProviderItemTax{
			ID:            lineID,
			RateID:        rate.ID,
			RateBps:       rate.RateBps,
			TaxAmount:     tax,
			TaxableAmount: amount - tax,
		}, nil
	}

	// A tax EXCLUSIVE stack: each component is computed on ITS OWN base and
	// rounded down SEPARATELY. Had a single rounding been done at the end, the
	// figures written per component would not equal their total — and an
	// invoice prints every component separately.
	var total int64
	// Components are carried ONLY on a stacked line: on a single-rate line the
	// list says nothing more than the line's own RateID/RateBps, and "there is
	// a list" would lose its meaning of "a stack taxed this line" (the
	// contract is in [validateComponents]).
	var components []TaxComponent
	if len(stack) > 1 {
		components = make([]TaxComponent, 0, len(stack))
	}
	for i := range stack {
		base := amount
		if stack[i].Compound {
			// A compound component's base includes the tax of the ones BELOW
			// it too; those taxes are already rounded, so no remainder is
			// carried up.
			base, err = addAmount(amount, total)
			if err != nil {
				return ProviderItemTax{}, err
			}
		}

		tax, taxErr := TaxOf(base, stack[i].RateBps)
		if taxErr != nil {
			return ProviderItemTax{}, taxErr
		}
		total, err = addAmount(total, tax)
		if err != nil {
			return ProviderItemTax{}, err
		}

		if components != nil {
			components = append(components, TaxComponent{
				RateID:        stack[i].ID,
				RateBps:       stack[i].RateBps,
				Compound:      stack[i].Compound,
				TaxableAmount: base,
				TaxAmount:     tax,
			})
		}
	}

	return ProviderItemTax{
		ID:      lineID,
		RateID:  stack[0].ID,
		RateBps: stack[0].RateBps,
		// The RATE the line carries is the stack's BASE and the amount is the
		// total: the base is a rate that was really applied, and it sits on an
		// amount that was really recorded. On a stacked line it is
		// INCOMPLETE — carrying all of the components is a separate slice —
		// but it is not WRONG.
		TaxAmount:     total,
		TaxableAmount: amount,
		// The components COMPLETE the line's rate, they do not replace it: the
		// line keeps carrying the stack's base, and the components write what
		// each rate took on its own base. These are the figures the invoice
		// will print (ADR 0096).
		Components: components,
	}, nil
}

// maxStackDepth is the most components a stack can carry.
//
// Four is one more than the deepest real stack known (federal + state +
// municipal + surcharge). The bound is not a performance measure but the limit
// of the CONFIGURATION's readability: nobody can check a five-tier tax chain by
// eye, and an unbounded chain would open a calculation of arbitrary size on a
// single line.
const maxStackDepth = 4

// stackFrom returns the stack starting from the selected rate, IN THE ORDER OF
// APPLICATION.
//
// The base is always first. The walk is over the rates IN MEMORY: all of the
// region's rates are already loaded (see [LocalProvider.loadRates]), so the
// expansion makes no extra query.
//
// # Why there is no CYCLE guard
//
// The walk starts from the SELECTED rate, and a selected rate stands on
// nothing — the ones standing on top never enter the candidate pool (see
// [newRateTable]). Every step advances through `standing`, and that map is
// derived from a rate's SINGLE stacks_on_id; so for the chain to come back to
// its start, the start would have to have a base too, and then it would not
// have been selected.
//
// That is why counting the ids seen here has no counterpart: it was written,
// measured to be impossible to trigger with any configuration, and then
// deleted. The walk is still BOUNDED by depth — a five-tier chain written by
// hand (bypassing the service) is possible, and instead of silently producing
// four components it stops.
func (t rateTable) stackFrom(base models.TaxRate) ([]models.TaxRate, error) {
	stack := make([]models.TaxRate, 0, maxStackDepth)

	current := base
	for {
		stack = append(stack, current)

		next, ok := t.standing[current.ID]
		if !ok {
			return stack, nil
		}
		if len(stack) >= maxStackDepth {
			return nil, errors.Internal(CodeInconsistentConfig,
				"the tax rate stack exceeds %d components (%s)", maxStackDepth, base.ID)
		}
		current = next
	}
}

// matchSpecificity returns the MOST SPECIFIC match of the rules with the keys;
// zero if there is no match.
func matchSpecificity(rules []models.TaxRateRule, keys []matchKey) int {
	best := 0
	for i := range rules {
		for _, key := range keys {
			if rules[i].Reference != key.reference || rules[i].ReferenceID != key.referenceID {
				continue
			}
			if spec := rules[i].Reference.Specificity(); spec > best {
				best = spec
			}
		}
	}
	return best
}
