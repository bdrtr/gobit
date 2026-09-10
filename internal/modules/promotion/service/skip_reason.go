package service

import (
	"slices"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// SkipReason names WHY a promotion did not enter the computation.
//
// # Why a typed word and not a sentence
//
// The reason is read by an operator through the admin endpoint, and an operator
// reading a free-text explanation cannot filter, count or alert on it. A closed
// set can be translated in the admin interface and can be counted: "eleven carts
// this hour were refused for currency_mismatch" is a question a sentence cannot
// answer.
//
// # Why the storefront does not get it
//
// It is published on the ADMIN compute endpoint alone, which asks for a scope.
// Telling a customer that their code exists but its campaign has not started
// hands a code guesser a campaign calendar — the same leak
// [Service.LookupStoreCoupon] refuses to open, and this surface must not open it
// from the side. The cross-module body does not carry it either, and that is the
// same decision seen from further away: the cart's totals are read by the
// storefront.
type SkipReason string

// The reasons a candidate is left out. They are the branches of [eligible], one
// word each, and the set is CLOSED: a branch added there without a word here
// would report a promotion as skipped for no stated reason.
const (
	// SkipNotActive is a promotion that is a draft or has been paused.
	SkipNotActive SkipReason = "not_active"
	// SkipMechanicUnknown is a promotion whose type is a word this engine does
	// not define.
	//
	// The database refuses it with a CHECK, so the state arrives only from a row
	// written by hand or from a constraint somebody dropped. It is named rather
	// than ignored because the alternative is worse than a refusal: an unknown
	// mechanic falling through to the standard one would apply a discount whose
	// shape nobody chose.
	SkipMechanicUnknown SkipReason = "mechanic_unknown"
	// SkipNoApplicationMethod is a promotion nobody ever told what to discount.
	SkipNoApplicationMethod SkipReason = "no_application_method"
	// SkipRewardMismatch is a promotion whose MECHANIC and whose application
	// method disagree: a buy-X-get-Y that does not say how many units are bought
	// and how many are rewarded, or a standard promotion whose method carries
	// those counts and has nothing to do with them.
	//
	// The two directions are ONE word for the reason [SkipCampaignClosed] merges
	// three: the merchant's answer is the same either way — make the mechanic and
	// the method agree — and the fix is in the same two places.
	//
	// Not applying is the safe direction. A half-configured reward that applied
	// would give away units nobody earned, and this one is visible instead: the
	// operator sees the word on the admin computation.
	SkipRewardMismatch SkipReason = "reward_mismatch"
	// SkipUsageExhausted is a promotion whose uses have run out.
	SkipUsageExhausted SkipReason = "usage_exhausted"
	// SkipCodeNotGiven is a coupon whose code was not among the ones sent.
	//
	// It reaches the computation at all because the candidate query returns the
	// promotions of the codes it was given AND every automatic one; a coupon that
	// arrives without its code is the belt beside that query's braces.
	SkipCodeNotGiven SkipReason = "code_not_given"
	// SkipCampaignClosed is a campaign that was deleted, is outside its window,
	// or has spent its budget. The three are ONE word on purpose: they are the
	// same answer to the merchant — the campaign is not paying today — and
	// splitting them would put the campaign's calendar into an answer.
	SkipCampaignClosed SkipReason = "campaign_closed"
	// SkipCurrencyMismatch is a fixed-amount discount, or a campaign budget,
	// quoted in a currency other than the cart's. No conversion is done; the
	// reason is in [ComputeInput.CurrencyCode].
	SkipCurrencyMismatch SkipReason = "currency_mismatch"
	// SkipRulesNotMatched is a promotion whose CONTEXT rules the cart does not
	// satisfy — the customer's group, the region, whatever the merchant wrote.
	SkipRulesNotMatched SkipReason = "rules_not_matched"
)

// SkippedPromotion is one promotion that was considered and left out.
//
// # The population
//
// Every automatic promotion and the promotions of the codes that were sent,
// WITHOUT the status filter — that last part is [Service.ExplainDiscounts]'s
// whole reason for existing. A coupon nobody typed stays out: an answer that grew
// with the catalog would say nothing about the cart that was asked about.
//
// So this list is "what was considered and refused" rather than "every promotion
// in the shop", and the difference is not a limitation but the question's own
// shape: the merchant is asking about ONE cart.
type SkippedPromotion struct {
	// PromotionID is the promotion that was left out.
	PromotionID string
	// Code is its coupon code, and it is ALWAYS set.
	//
	// Every promotion in this module has one — normalizeCode requires at least
	// three characters — and being automatic does not change that; it changes
	// only whether the code has to be TYPED for the promotion to apply.
	Code string
	// Reason is why. It is one of the [SkipReason] constants.
	Reason SkipReason
}

// skipReasonOf returns the reason a candidate is left out, or the empty string
// when it is eligible.
//
// It is the ONE place the elimination is decided; [eligible] is written in terms
// of it. Two functions answering "may this apply" separately would drift, and the
// drift would be silent in the worst direction: a promotion applied for one
// reason and reported skipped for another.
func skipReasonOf(candidate models.PromotionCandidate, in ComputeInput) SkipReason {
	promo := candidate.Promotion
	switch {
	case promo.Status != models.PromotionActive:
		return SkipNotActive
	case !promo.Type.Valid():
		return SkipMechanicUnknown
	case candidate.Method == nil:
		return SkipNoApplicationMethod
	case !mechanicMatchesMethod(promo, candidate.Method):
		return SkipRewardMismatch
	case promo.UsageExhausted():
		return SkipUsageExhausted
	case !promo.IsAutomatic && !slices.Contains(in.Codes, promo.Code):
		return SkipCodeNotGiven
	case !campaignUsable(candidate, in.At):
		return SkipCampaignClosed
	case !campaignBudgetCurrencyMatches(candidate, in.CurrencyCode):
		return SkipCurrencyMismatch
	case candidate.Method.Type == models.MethodFixed &&
		candidate.Method.CurrencyCode != in.CurrencyCode:
		return SkipCurrencyMismatch
	case !matchRules(candidate.ContextRules(), in.Context):
		return SkipRulesNotMatched
	}

	return ""
}
