package service

import (
	"context"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// CalculateParams is the context of a price calculation.
type CalculateParams struct {
	// CurrencyCode is the requested currency (ISO 4217); it is required.
	CurrencyCode string
	// Quantity is the quantity to be bought; 0 is taken as 1.
	Quantity int32
	// Attributes is the rule context (e.g. {"region_id": "reg_1"}).
	// A rule whose field is NOT here does not match.
	Attributes map[string]string
	// At is the moment of the calculation; zero means "now". The date window
	// of the price lists is evaluated against this moment.
	At time.Time
}

// CalculatePrice selects the price of a price set that is VALID in the given
// context.
//
// This function is the selection point the cart total rests on, which is why
// the rule is defined here, in one place, and explicitly.
//
// # 1. Elimination
//
// A price does not enter the race at all unless it satisfies ALL of these:
//
//   - Its currency is exactly the one asked for (compared in UPPER case).
//   - The quantity is inside the price's [MinQuantity, MaxQuantity] range (a nil
//     upper bound is unbounded).
//   - If the price belongs to a list, the list must be USABLE: its status is
//     active and the moment is inside the list's date window. A price whose
//     list was deleted is eliminated too.
//   - ALL of the price's rules match the context. A rule whose field is not in
//     the context does not match, so the price is eliminated.
//
// # 2. Ranking
//
// Among the survivors these criteria are looked at in order; the first
// DIFFERENCE decides the winner:
//
//  1. List priority (larger wins): override (2) > sale (1) > base price (0). A
//     contract/B2B price overrides the campaign, and the campaign overrides the
//     base price.
//  2. The buyer (narrower wins): a price whose rule names the customer beats one
//     that names their company, and that beats one that names neither
//     ([models.PriceRule.BuyerRank], ADR 0185). A contract is the merchant's
//     decision for that buyer, and a segment price the amount rung below finds
//     cheaper must not undo it — the reason ADR 0049 gave for ranking groups
//     rather than letting the cheapest one win.
//  3. The number of matched rules (more wins): a price satisfying more
//     conditions is more SPECIFIC; a "TR region + VIP group" price beats a "TR
//     region" price.
//  4. The width of the quantity range (narrower wins): a 10-20 range beats a
//     1-unbounded range. This is what makes wholesale tiers work.
//  5. The amount (smaller wins): at equal specificity the decision goes IN THE
//     CUSTOMER'S FAVOR.
//  6. The id (smaller wins): in every remaining case the result is
//     DETERMINISTIC. Ids are time ordered, so this means "the one written first
//     wins".
//
// If no candidate is left, errors.NotFound (code [CodeNotCalculable]) is
// returned; it means "there is no price in this currency/quantity" and it is a
// DIFFERENT case from the price set not existing (that is NotFound too, with a
// different code).
func (s *Service) CalculatePrice(
	ctx context.Context,
	priceSetID string,
	params CalculateParams,
) (models.CalculatedPrice, error) {
	if err := s.ready(); err != nil {
		return models.CalculatedPrice{}, err
	}
	if err := requireID(priceSetID, models.PriceSetIDPrefix, "price set id"); err != nil {
		return models.CalculatedPrice{}, err
	}

	currency, err := normalizeCurrency(params.CurrencyCode)
	if err != nil {
		return models.CalculatedPrice{}, err
	}
	quantity, err := normalizeQuantity(params.Quantity)
	if err != nil {
		return models.CalculatedPrice{}, err
	}

	at := params.At
	if at.IsZero() {
		at = s.clock()
	} else {
		at = at.UTC()
	}

	candidates, err := s.repo.ListPriceCandidates(ctx, priceSetID)
	if err != nil {
		return models.CalculatedPrice{}, err
	}
	if len(candidates) == 0 {
		// A set with no prices is told apart in two cases: the set does not
		// exist (404, price_set_not_found) or it exists and is empty (404,
		// price_not_calculable). The extra query is made ONLY on this path; the
		// happy path is a single round trip.
		if _, err := s.repo.GetPriceSet(ctx, priceSetID); err != nil {
			return models.CalculatedPrice{}, err
		}
	}

	selected, ok := selectPrice(candidates, currency, quantity, params.Attributes, at)
	if !ok {
		return models.CalculatedPrice{}, errors.NotFound(CodeNotCalculable,
			"%s has no valid price in %s for a quantity of %d",
			priceSetID, currency, quantity).
			WithDetails(map[string]any{
				"price_set_id":  priceSetID,
				"currency_code": currency,
				"quantity":      quantity,
			})
	}
	return selected, nil
}

// normalizeQuantity validates the quantity parameter and applies the default.
func normalizeQuantity(quantity int32) (int32, error) {
	if quantity == 0 {
		return models.MinQuantity, nil
	}
	if quantity < models.MinQuantity {
		return 0, errors.Invalid(CodeInvalidInput,
			"the quantity has to be at least %d, %d given", models.MinQuantity, quantity)
	}
	if quantity > models.MaxQuantity {
		return 0, errors.Invalid(CodeInvalidInput,
			"the quantity can be at most %d, %d given", models.MaxQuantity, quantity)
	}
	return quantity, nil
}

// scored is the criteria of one candidate in the ranking.
//
// The criteria are derived from the candidate ONCE and not recomputed during
// the comparison. This keeps the ranking rule readable in one place.
type scored struct {
	candidate models.PriceCandidate
	// tier is the list priority (override 2 > sale 1 > base 0).
	tier int
	// buyer is how narrowly the price names the buyer (customer 2 > company 1
	// > nobody 0).
	buyer int
	// rules is the number of matched rules; it measures specificity.
	rules int
	// span is the width of the quantity range; the maximum for an unbounded one.
	span int64
}

// selectPrice selects the winner among the eligible candidates.
//
// It is a PURE function: it touches neither the database, the clock nor the
// logger. Every branch of the selection rule can therefore be proven by a unit
// test without a database. The second return value is false when no candidate
// is eligible.
func selectPrice(
	candidates []models.PriceCandidate,
	currency string,
	quantity int32,
	attributes map[string]string,
	at time.Time,
) (models.CalculatedPrice, bool) {
	var best scored
	found := false

	for i := range candidates {
		candidate := candidates[i]
		if !eligible(candidate, currency, quantity, attributes, at) {
			continue
		}
		current := score(candidate)
		if !found || better(current, best) {
			best, found = current, true
		}
	}
	if !found {
		return models.CalculatedPrice{}, false
	}
	return result(best, quantity), true
}

// eligible reports whether a candidate can enter the race (see "Elimination"
// in the CalculatePrice godoc).
func eligible(
	candidate models.PriceCandidate,
	currency string,
	quantity int32,
	attributes map[string]string,
	at time.Time,
) bool {
	price := candidate.Price
	if price.CurrencyCode != currency {
		return false
	}
	if quantity < price.MinQuantity {
		return false
	}
	if price.MaxQuantity != nil && quantity > *price.MaxQuantity {
		return false
	}
	if !listAvailable(candidate, at) {
		return false
	}
	return matchRules(price.Rules, attributes)
}

// listAvailable reports whether the list the candidate belongs to can offer a
// price at the given moment; a price with no list (the base price) always can.
//
// If the list id is set but its record is missing, the list was DELETED; the
// price is orphaned and not counted.
//
// It is a separate function on purpose: [QueryProvider] applies the same
// filter. If the rule did not stay in one place, the price the module computes
// and the price it shows the storefront would drift apart.
func listAvailable(candidate models.PriceCandidate, at time.Time) bool {
	if candidate.Price.PriceListID == nil {
		return true
	}
	return candidate.List != nil && candidate.List.Usable(at)
}

// score derives the candidate's ranking criteria.
func score(candidate models.PriceCandidate) scored {
	tier := 0
	if candidate.Price.PriceListID != nil && candidate.List != nil {
		tier = candidate.List.Type.Priority()
	}
	buyer := 0
	for i := range candidate.Price.Rules {
		buyer = max(buyer, candidate.Price.Rules[i].BuyerRank())
	}
	return scored{
		candidate: candidate,
		tier:      tier,
		buyer:     buyer,
		rules:     len(candidate.Price.Rules),
		span:      quantitySpan(candidate.Price),
	}
}

// quantitySpan is the width of the quantity range; the maximum when there is no
// upper bound.
//
// Counting an unbounded range as the widest follows from the "narrower wins"
// rule: an unbounded range is always the most general candidate.
func quantitySpan(price models.Price) int64 {
	if price.MaxQuantity == nil {
		return math.MaxInt64
	}
	return int64(*price.MaxQuantity) - int64(price.MinQuantity)
}

// better reports whether a beats b.
//
// The order of the criteria is defined in the CalculatePrice godoc; the first
// DIFFERENCE decides. The last criterion is the id, which is never equal (a
// primary key), so the ordering is TOTAL: the result does not depend on the
// order the candidates arrive in.
func better(a, b scored) bool {
	if a.tier != b.tier {
		return a.tier > b.tier
	}
	if a.buyer != b.buyer {
		return a.buyer > b.buyer
	}
	if a.rules != b.rules {
		return a.rules > b.rules
	}
	if a.span != b.span {
		return a.span < b.span
	}
	if a.candidate.Price.Amount != b.candidate.Price.Amount {
		return a.candidate.Price.Amount < b.candidate.Price.Amount
	}
	return a.candidate.Price.ID < b.candidate.Price.ID
}

// result turns the winning candidate into the result model.
func result(best scored, quantity int32) models.CalculatedPrice {
	price := best.candidate.Price

	var listType models.PriceListType
	if price.PriceListID != nil && best.candidate.List != nil {
		listType = best.candidate.List.Type
	}

	maxQty := price.MaxQuantity
	if maxQty != nil {
		limit := *maxQty
		maxQty = &limit
	}

	return models.CalculatedPrice{
		PriceID:       price.ID,
		PriceSetID:    price.PriceSetID,
		CurrencyCode:  price.CurrencyCode,
		Amount:        price.Amount,
		Quantity:      quantity,
		Total:         price.Amount * int64(quantity),
		MinQuantity:   price.MinQuantity,
		MaxQuantity:   maxQty,
		PriceListID:   price.PriceListID,
		PriceListType: listType,
		MatchedRules:  best.rules,
	}
}

// matchRules reports whether ALL of the price's rules match the context.
// A price with no rules is unconditional and always matches.
func matchRules(rules []models.PriceRule, attributes map[string]string) bool {
	for i := range rules {
		if !matchRule(rules[i], attributes) {
			return false
		}
	}
	return true
}

// matchRule reports whether a single rule matches the context.
//
// If the field the rule looks at is NOT in the context, the rule does not
// match — even for negative operators such as "ne" (not equal). Otherwise a
// request with an empty context would satisfy every negative rule and open the
// segment prices to everybody.
//
// A rule with NO VALUES does not match either, and it DOES NOT PANIC. Service
// validation never produces such a record, but the calculation has to survive
// every row it reads from the database: a maintenance script running raw SQL or
// a partial restore can leave the values empty. The reason is the same as for
// an unknown operator — a condition that cannot be read must not silently
// disable the rule and OPEN the price to everybody.
func matchRule(rule models.PriceRule, attributes map[string]string) bool {
	if len(rule.Values) == 0 {
		return false
	}

	value, ok := attributes[rule.Attribute]
	if !ok {
		return false
	}

	switch rule.Operator {
	case models.OpEq:
		return value == rule.Values[0]
	case models.OpNe:
		return value != rule.Values[0]
	case models.OpIn:
		return slices.Contains(rule.Values, value)
	case models.OpNin:
		return !slices.Contains(rule.Values, value)
	case models.OpGt, models.OpGte, models.OpLt, models.OpLte:
		return matchNumeric(rule, value)
	default:
		// An unknown operator DOES NOT MATCH: a value that later leaked into the
		// database must not silently disable the rule and open the price to
		// everybody.
		return false
	}
}

// matchNumeric evaluates the numeric operators.
//
// Both sides have to convert to an integer; a context value that does not
// convert makes the rule not match (it produces no error): the context comes
// from outside, and a single broken field must not bring down the whole price
// calculation.
//
// It is called ONLY from matchRule, where the rule is guaranteed at least one
// value, which is why the first value is read directly.
func matchNumeric(rule models.PriceRule, value string) bool {
	left, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return false
	}
	right, err := strconv.ParseInt(rule.Values[0], 10, 64)
	if err != nil {
		return false
	}

	switch rule.Operator {
	case models.OpGt:
		return left > right
	case models.OpGte:
		return left >= right
	case models.OpLt:
		return left < right
	case models.OpLte:
		return left <= right
	case models.OpEq, models.OpNe, models.OpIn, models.OpNin:
		return false
	default:
		return false
	}
}
