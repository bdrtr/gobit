package service

import (
	"cmp"
	"slices"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// This file is the "buy N, get M" mechanic (ADR 0112).
//
// It is the one promotion type that counts UNITS rather than measuring amounts,
// which is why it lives beside the arithmetic in compute.go rather than inside
// it: everything here is about which units were bought, which units are left,
// and which of the ones left are the cheapest.

// mechanicMatchesMethod reports whether a promotion's MECHANIC and its application
// method agree about rewarding a purchase.
//
// A buyget needs the pair of counts and a standard promotion must not carry them.
// The rule has ONE home because it is asked in two places — the computation's
// elimination and the coupon the storefront may be shown — and a coupon offered
// under one reading while the computation refuses it under another is exactly the
// drift the elimination's own godoc warns about.
//
// A promotion with no method agrees with nothing: there is no half to agree with.
func mechanicMatchesMethod(promo models.Promotion, method *models.ApplicationMethod) bool {
	if method == nil {
		return false
	}
	return (promo.Type == models.PromotionBuyGet) == method.RewardsPurchase()
}

// applyBuyGet applies a buyget promotion to the lines and returns the discount it
// ACTUALLY produced.
//
// # The order of the two questions
//
// The purchase is counted first and the reward is paid second, and the two look
// at different sets: the buy rules choose the lines that COUNT, the target rules
// choose the lines that are REWARDED. A promotion with no buy rule counts every
// line, which is the "buy any three" shape.
//
// # A bought unit is not also a rewarded unit
//
// The units the purchase consumed are reserved and cannot be given away again,
// so "buy two, get one" needs THREE units in the cart and not two. The other
// reading — where the same two units both satisfy the condition and collect the
// reward — hands the shopper two for the price of one under the same wording,
// and no merchant means that.
//
// Which units are consumed is decided by price: the MOST EXPENSIVE units satisfy
// the purchase, and the cheapest are left for the reward. It is the supermarket's
// own rule, and it is also the merchant-safe direction — the reward is the
// smallest one the promotion's wording allows.
//
// # It is granted ONCE
//
// A cart holding six units under "buy two, get one" is rewarded one unit, not
// two. The repeating ladder is a different promise ("every third one free"), it
// needs its own word in the record, and nothing asks for it yet; the trigger is a
// merchant writing one down.
func applyBuyGet(candidate models.PromotionCandidate, items []lineState, targets []*lineState) int64 {
	method := *candidate.Method
	// The elimination refuses a buyget whose method carries no counts and reports
	// it as skipped ([SkipRewardMismatch]); this is the belt beside that brace,
	// because a nil dereference here would take the whole cart down.
	if !method.RewardsPurchase() {
		return 0
	}

	bought := filterLines(items, candidate.BuyRules())
	var count int64
	for _, line := range bought {
		count += line.quantity
	}
	if count < *method.BuyQuantity {
		return 0
	}

	reserved := reserveBoughtUnits(bought, *method.BuyQuantity)
	return payReward(method, targets, reserved)
}

// reserveBoughtUnits marks the units the purchase consumed, MOST EXPENSIVE first.
//
// The map is keyed by the line's ADDRESS and not by its id. The two lists a
// computation carries — items and shipping methods — are checked for duplicates
// SEPARATELY, so nothing forbids an item and a shipping method sharing an id, and
// a name-keyed map would let a purchase made of goods reserve units of a shipping
// method that merely spells its identifier the same way.
func reserveBoughtUnits(lines []*lineState, quantity int64) map[*lineState]int64 {
	order := slices.Clone(lines)
	slices.SortFunc(order, func(a, b *lineState) int {
		if byPrice := cmp.Compare(b.unitAmount, a.unitAmount); byPrice != 0 {
			return byPrice
		}
		// The identifier breaks the tie so that two equally priced lines are
		// consumed in a defined order; without it the result would depend on the
		// order the caller happened to send.
		return cmp.Compare(a.id, b.id)
	})

	reserved := make(map[*lineState]int64, len(order))
	left := quantity
	for _, line := range order {
		if left <= 0 {
			break
		}
		take := min(line.quantity, left)
		reserved[line] = take
		left -= take
	}
	return reserved
}

// payReward discounts the CHEAPEST available units of the target lines and
// returns what was actually applied.
//
// A unit whose reward is worth nothing does not consume a slot: a line priced at
// zero would otherwise eat the whole reward and hand the shopper nothing, and
// because the walk is cheapest-first those lines are exactly the ones it meets
// before the rest.
func payReward(method models.ApplicationMethod, targets []*lineState, reserved map[*lineState]int64) int64 {
	order := slices.Clone(targets)
	slices.SortFunc(order, func(a, b *lineState) int {
		if byPrice := cmp.Compare(a.unitAmount, b.unitAmount); byPrice != 0 {
			return byPrice
		}
		return cmp.Compare(a.id, b.id)
	})

	left := *method.ApplyToQuantity
	var applied int64
	for _, line := range order {
		if left <= 0 {
			break
		}
		available := line.quantity - reserved[line]
		if available <= 0 {
			continue
		}
		perUnit := rewardPerUnit(method, line.unitAmount)
		if perUnit <= 0 {
			continue
		}
		take := min(available, left)
		applied += line.charge(take * perUnit)
		left -= take
	}
	return applied
}

// rewardPerUnit is what ONE rewarded unit is worth.
//
// A fixed amount is capped at the unit's own price: a 100 discount on a unit
// priced 30 discounts 30, and the 70 does not spill onto the next unit of the
// same line. The line's own ceiling ([lineState.charge]) would catch the spill
// eventually, but only after it had been counted as reward for units it never
// paid for.
//
// The percentage is taken of the UNIT and rounds down like every other
// percentage here (see [models.BasisPointDenominator]); "free" is 10000 basis
// points, which is why the mechanic needs no third measure of its own.
//
// The product cannot overflow: at most [models.MaxQuantity] units (10^6) at most
// [models.MaxAmount] each (10^12) is 10^18, inside int64.
func rewardPerUnit(method models.ApplicationMethod, unitAmount int64) int64 {
	switch method.Type {
	case models.MethodFixed:
		return min(method.Value, unitAmount)
	case models.MethodPercentage:
		return percentageOf(unitAmount, method.Value)
	default:
		// An unrecognized measure rewards NOTHING, for the same reason an
		// unrecognized target selects no line: a value that leaked into the
		// database later must not become a discount nobody defined.
		return 0
	}
}
