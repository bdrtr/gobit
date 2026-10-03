package service

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// MaxComputeLines is the maximum number of lines a single computation can
// carry.
//
// The bound has to exist: every line is evaluated separately for every
// promotion, and an unbounded list would keep the computation busy with a
// single request. The value is generous — a real cart's line count is far
// below it, and the bound exists only to stop a broken client.
const MaxComputeLines = 1000

// ComputeItem is a single cart item taking part in a computation.
type ComputeItem struct {
	// ID is the item's identity; the discount comes back in the result under
	// this identity, and it CANNOT REPEAT within the same list.
	ID string
	// Amount is the item's subtotal (unit × quantity), minor unit.
	//
	// The discount is applied to the line and this is its base: recomputed from
	// the unit price, it would produce a base different from the caller's for
	// quantities that do not divide.
	Amount int64
	// UnitAmount is the item's UNIT price (minor unit) and is MANDATORY:
	// UnitAmount × Quantity has to equal Amount.
	//
	// The caller SENDS the unit price; this package does NOT DERIVE it.
	// Deriving it is a single division, and it is silent exactly there: three
	// units worth 100 cents round to 33 cents each, and a "buy 2, get one free"
	// promotion would give the customer one cent less than was promised. The
	// sending side already has the number — the cart is the side that chooses
	// the unit price — and not sending it would make the receiver guess a known
	// number.
	//
	// The identity is kept MANDATORY rather than left as an optional field:
	// making a field that only the "buyget" mechanic reads skippable would have
	// meant that promotion silently not working for some callers.
	UnitAmount int64
	// Quantity is the item's quantity; for a "fixed" + "each" discount it
	// decides how many units the discount applies to.
	Quantity int64
	// Attributes are the item attributes the target rules look at
	// (e.g. {"product_category_id": "cat_1"}). It may be nil; in that case a
	// promotion with a target rule cannot select this item.
	Attributes map[string]string
	// Lists are the item attributes the target rule reads as a SET
	// (e.g. {"category_ids": ["cat_1", "cat_2"]}), ADR 0148.
	//
	// It is Attributes' sibling, NOT its replacement: only the `any_in` operator
	// looks here, and the operators that look at a single value NEVER look here.
	// The answer of a shipped `eq` rule cannot change because the list has
	// started to arrive.
	Lists map[string][]string
}

// ComputeShippingMethod is a single shipping method taking part in a
// computation.
//
// It does NOT CARRY a quantity: a shipping method has no quantity and counts
// as one unit in a "fixed" + "each" discount.
type ComputeShippingMethod struct {
	// ID is the shipping method's identity; it CANNOT REPEAT within the same
	// list.
	ID string
	// Amount is the shipping amount (minor unit).
	Amount int64
	// Attributes are the attributes the target rules look at; it may be nil.
	Attributes map[string]string
}

// ComputeInput is the context of a discount computation.
type ComputeInput struct {
	// CurrencyCode is the cart's currency (ISO 4217); it is MANDATORY.
	//
	// Fixed-amount discounts are applied only in their OWN currency; a
	// promotion in a different currency is eliminated. Currency conversion is
	// not promotion's job, and a silent conversion would apply a 100 USD
	// discount as 100 TRY.
	CurrencyCode string
	// Context holds the fields the context rules look at (e.g.
	// {"region_id": "reg_1", "customer_group_id": "vip"}). It may be nil; in
	// that case every promotion with a context rule is eliminated.
	Context map[string]string
	// ContextLists holds the fields the context rule reads on the LIST side
	// (e.g. {"customer_group_id": ["retail", "vip"]}).
	//
	// It is an additional field standing BESIDE [Context], NOT a type change
	// that replaces it: the body the caller sends is read with
	// `DisallowUnknownFields`, so a rename or a type change would break every
	// caller — and the answer of a shipped `in` rule has to stay the same.
	//
	// Only an operator for which [models.RuleOperator.ReadsAList] is true looks
	// here. It may be nil; in that case every rule with such an operator does
	// not match, which is the right answer: if the list was not sent, which
	// groups the customer is in is NOT KNOWN.
	ContextLists map[string][]string
	// Items are the cart items.
	Items []ComputeItem
	// ShippingMethods are the cart's shipping methods.
	ShippingMethods []ComputeShippingMethod
	// Codes are the coupon codes to apply; their order DOES NOT AFFECT THE
	// RESULT (see [Service.ComputeDiscounts], "Order").
	Codes []string
	// At is the moment the computation is made for; if zero, "now" is used.
	// The campaigns' date windows are evaluated against this moment.
	At time.Time
}

// LineDiscount is the discount that falls to a single line.
type LineDiscount struct {
	// ID is the line's identity.
	ID string
	// Amount is the TOTAL discount that falls to the line (minor unit); it
	// NEVER exceeds the line's amount.
	Amount int64
}

// AppliedPromotion is a promotion that actually produced a discount in the
// computation.
type AppliedPromotion struct {
	// PromotionID is the promotion's identity.
	PromotionID string
	// Code is the promotion's coupon code.
	Code string
	// IsAutomatic reports whether the promotion was applied without a code.
	IsAutomatic bool
	// Amount is the total discount the promotion ACTUALLY applied; the part
	// caught on the line bounds DOES NOT GO IN HERE.
	Amount int64
}

// ComputeResult is the result of a discount computation.
//
// The identity always holds:
//
//	DiscountTotal = ItemsDiscountTotal + ShippingDiscountTotal
//	              = Σ Items[i].Amount + Σ ShippingMethods[i].Amount
//	              = Σ Applied[i].Amount
type ComputeResult struct {
	// CurrencyCode is the computation's currency (UPPER case).
	CurrencyCode string
	// Items are the per-item discounts; it holds one record for EVERY item in
	// the input (including those whose discount is zero) and is in the SAME
	// order as the input.
	Items []LineDiscount
	// ShippingMethods are the per-shipping-method discounts; the same rule
	// applies.
	ShippingMethods []LineDiscount
	// ItemsDiscountTotal is the total discount that falls to the items.
	ItemsDiscountTotal int64
	// ShippingDiscountTotal is the total discount that falls to the shipping
	// methods.
	ShippingDiscountTotal int64
	// DiscountTotal is the total discount.
	DiscountTotal int64
	// Applied are the promotions that actually produced a discount, IN
	// APPLICATION ORDER.
	Applied []AppliedPromotion
	// Skipped are the candidates that were NOT TAKEN INTO the computation, and
	// each carries a reason.
	//
	// The population is the candidates the query returned: every automatic
	// promotion and the promotions of the codes sent. A promotion whose code
	// was not entered and that is not automatic either never enters the
	// database read — so this list is "evaluated and rejected", not "every
	// promotion in the shop".
	//
	// It is published ONLY on the admin endpoint; the reasoning is in the
	// [SkipReason] godoc.
	Skipped []SkippedPromotion
	// UnmatchedCodes are the coupon codes that could not be tied to an
	// applicable promotion.
	//
	// The code may be wrong, the promotion may be draft/inactive, its campaign
	// may have ended or its budget may be exhausted; NO DISTINCTION IS MADE.
	// The reason is leakage: the answer "this code exists but its campaign has
	// not started yet" would give away the existence of an unpublished
	// campaign.
	UnmatchedCodes []string
}

// ComputeDiscounts computes the discounts for the given cart context.
//
// IT IS THE HEART OF THIS MODULE and IT WRITES NOTHING: the coupon counter and
// the campaign budget change only through [Service.RedeemPromotion]. The
// separation is mandatory — the cart total is recomputed on every change, and
// every computation consuming a coupon would make looking at the cart and
// spending the coupon the same thing.
//
// # 1. Elimination
//
// A promotion does not enter the computation at all unless it satisfies ALL of
// these conditions:
//
//   - Its status is "active". Draft and inactive promotions produce no
//     discount.
//   - Its type is DEFINED and AGREES with the application method: if it is
//     "buyget", the method carries the buy and reward quantities, and if it is
//     "standard", it does not. A half-finished reward produces no discount and
//     says why (see [SkipRewardMismatch]); the mechanic itself is in
//     [applyBuyGet].
//   - It has an application method. A promotion without a method does not say
//     HOW the discount is to be applied and is skipped.
//   - Its usage limit is NOT REACHED.
//   - If it has a campaign: the campaign must NOT BE DELETED, its date window
//     must COVER the moment, and its budget must NOT BE EXHAUSTED.
//   - If its campaign's budget is measured in MONEY, the budget's currency is
//     the SAME as the cart's. Otherwise the discount would show in the cart but
//     [Service.RedeemPromotion] would refuse it (see
//     [campaignBudgetCurrencyMatches]).
//   - If it is a fixed-amount discount, its currency is the SAME as the cart's.
//   - ALL of its context rules match the cart context.
//
// An additional condition for coupon codes: the promotion that owns the code
// enters the computation only when the code is given. Automatic promotions
// enter without a code.
//
// # 2. Order — COUPONS FIRST, AUTOMATICS AFTER
//
// The application order is this, and it is INDEPENDENT of the caller's code
// order:
//
//  1. Coupon-code promotions, in ascending order of identity.
//  2. Automatic promotions, in ascending order of identity.
//
// Since identities are time-ordered, the second criterion means "the one
// defined first is applied first", and the result is DETERMINISTIC.
//
// Coupons coming first is not a preference but an explainability decision:
// the order only becomes visible when a line's discount REACHES its amount, and
// at that moment the promotion that gets clipped is the last one. The customer
// must see the coupon they typed applied in full; an automatic discount they
// never heard of being silently clipped is a question nobody asks.
//
// # 3. Percentages DO NOT STACK ON TOP OF EACH OTHER (not compound)
//
// Every percentage discount is computed on the line's ORIGINAL amount, not on
// the amount LEFT OVER from earlier discounts. When 10% and 20% are applied
// together, the total discount is 30%, not 28%.
//
// The reasoning is threefold:
//
//   - Explainability: the customer adds two discounts, they do not multiply
//     them. A compound computation always gives less than was promised and
//     turns into a support ticket.
//   - Order independence: in a compound computation the result depends on the
//     application order; in a non-compound one the order only shows when the
//     upper bound binds.
//   - The upper bound is already guaranteed (see below), so the one thing a
//     compound computation protects (the discount not exceeding the amount) is
//     provided by another rule.
//
// # 4. Upper bounds
//
// Two invariants hold under every condition:
//
//   - A line's TOTAL discount CANNOT EXCEED the line's amount. The excess
//     drops and DOES NOT GO INTO the promotion's [AppliedPromotion.Amount].
//   - The total discount cannot exceed the subtotal; this is the natural
//     consequence of the line bound (Σ line discount ≤ Σ line amount).
//
// The clipped part of the bound is NOT CARRIED to ANOTHER line: carrying it
// would give that line more than the promotion promised.
//
// # 5. Rounding
//
// The percentage computation is integer and rounds DOWN (see
// [models.BasisPointDenominator]). In an "across" allocation the total is
// rounded ONCE and then distributed to the lines exactly — who the leftover
// cent goes to is defined in the [allocateAcross] godoc.
//
// # 6. Campaign budget
//
// A promotion whose campaign budget is EXHAUSTED is not applied at all; NO
// PARTIAL application is made. The reason is that this call has no side
// effects: sharing out the remaining budget here would show the customer a
// number that can change between computation and redemption as if it were
// final. The real arbiter of the budget is [Service.RedeemPromotion], and
// errors.Conflict is returned there if the bound is exceeded.
func (s *Service) ComputeDiscounts(ctx context.Context, in ComputeInput) (ComputeResult, error) {
	if err := s.ready(); err != nil {
		return ComputeResult{}, err
	}

	normalized, err := normalizeComputeInput(in, s.clock())
	if err != nil {
		return ComputeResult{}, err
	}

	candidates, err := s.repo.ListCandidates(ctx, normalized.Codes)
	if err != nil {
		return ComputeResult{}, err
	}
	return computeDiscounts(candidates, normalized), nil
}

// ExplainDiscounts makes the same computation and also says WHY a promotion
// was not applied; it writes nothing.
//
// Its one difference from [Service.ComputeDiscounts] is the CANDIDATE READ: it
// uses the read without a status filter
// (repository.ListCandidatesForDiagnosis). The reason is for
// [ComputeResult.Skipped] to be useful — the filtered read never returns a
// promotion that has not been published, so the MOST COMMON answer to "I typed
// the code and nothing happened" ("you have not activated it") could not be
// given.
//
// # The DISCOUNT is the same
//
// The amounts of the two paths have to be EXACTLY the same, and they are: the
// extra members of the wider set cannot pass the elimination, so the set of
// applied promotions does not change. The computation itself is the same pure
// function ([computeDiscounts]); the only thing that differs is the read. This
// claim is pinned by a test — their diverging would have meant the discount
// shown to the merchant differing from the one the customer sees.
//
// The admin endpoint calls this; the primitive surface the cart flow calls
// DOES NOT, and the reason is leakage: the storefront reads the cart's totals
// (ADR 0110).
func (s *Service) ExplainDiscounts(ctx context.Context, in ComputeInput) (ComputeResult, error) {
	if err := s.ready(); err != nil {
		return ComputeResult{}, err
	}

	normalized, err := normalizeComputeInput(in, s.clock())
	if err != nil {
		return ComputeResult{}, err
	}

	candidates, err := s.repo.ListCandidatesForDiagnosis(ctx, normalized.Codes)
	if err != nil {
		return ComputeResult{}, err
	}

	return computeDiscounts(candidates, normalized), nil
}

// normalizeComputeInput validates the input and returns a normalized COPY of
// it.
//
// The copy is required: the codes are upper-cased and deduplicated, and
// modifying the caller's slice in place would corrupt data that belongs to the
// sender of the request.
func normalizeComputeInput(in ComputeInput, now time.Time) (ComputeInput, error) {
	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return ComputeInput{}, err
	}

	if len(in.Items) > MaxComputeLines {
		return ComputeInput{}, errors.Invalid(CodeInvalidInput,
			"a computation can carry at most %d items, %d given", MaxComputeLines, len(in.Items))
	}
	if len(in.ShippingMethods) > MaxComputeLines {
		return ComputeInput{}, errors.Invalid(CodeInvalidInput,
			"a computation can carry at most %d shipping methods, %d given",
			MaxComputeLines, len(in.ShippingMethods))
	}

	items := make([]ComputeItem, 0, len(in.Items))
	seen := make(map[string]struct{}, len(in.Items))
	var itemsSubtotal int64
	for i := range in.Items {
		item := in.Items[i]
		if err := validateLineID("item id", item.ID, seen); err != nil {
			return ComputeInput{}, withIndex(err, detailItemIndex, i)
		}
		if err := validateAmount("item amount", item.Amount); err != nil {
			return ComputeInput{}, withIndex(err, detailItemIndex, i)
		}
		if err := validateQuantity("item quantity", item.Quantity); err != nil {
			return ComputeInput{}, withIndex(err, detailItemIndex, i)
		}
		if err := validateUnitAmount(item); err != nil {
			return ComputeInput{}, withIndex(err, detailItemIndex, i)
		}
		itemsSubtotal += item.Amount
		if itemsSubtotal > models.MaxAmount {
			return ComputeInput{}, errors.Invalid(CodeInvalidInput,
				"the item subtotal can be at most %d (minor unit)", models.MaxAmount)
		}
		item.Attributes = maps.Clone(item.Attributes)
		items = append(items, item)
	}

	shipping := make([]ComputeShippingMethod, 0, len(in.ShippingMethods))
	seenShipping := make(map[string]struct{}, len(in.ShippingMethods))
	var shippingSubtotal int64
	for i := range in.ShippingMethods {
		method := in.ShippingMethods[i]
		if err := validateLineID("shipping method id", method.ID, seenShipping); err != nil {
			return ComputeInput{}, withIndex(err, detailShippingIndex, i)
		}
		if err := validateAmount("shipping amount", method.Amount); err != nil {
			return ComputeInput{}, withIndex(err, detailShippingIndex, i)
		}
		shippingSubtotal += method.Amount
		if shippingSubtotal > models.MaxAmount {
			return ComputeInput{}, errors.Invalid(CodeInvalidInput,
				"the shipping subtotal can be at most %d (minor unit)", models.MaxAmount)
		}
		method.Attributes = maps.Clone(method.Attributes)
		shipping = append(shipping, method)
	}

	codes, err := normalizeCodes(in.Codes)
	if err != nil {
		return ComputeInput{}, err
	}

	at := in.At
	if at.IsZero() {
		at = now
	} else {
		at = at.UTC()
	}

	return ComputeInput{
		CurrencyCode:    currency,
		Context:         maps.Clone(in.Context),
		ContextLists:    cloneLists(in.ContextLists),
		Items:           items,
		ShippingMethods: shipping,
		Codes:           codes,
		At:              at,
	}, nil
}

// normalizeCodes validates the coupon codes, converts them to UPPER case and
// DEDUPLICATES them.
//
// Deduplication is required: had the same code been given twice, the
// promotion would be applied twice and the discount doubled. The order is kept
// so that the index in the error message is meaningful; the application order
// is independent of the codes anyway.
func normalizeCodes(codes []string) ([]string, error) {
	if len(codes) == 0 {
		return []string{}, nil
	}
	if len(codes) > MaxCodesPerCompute {
		return nil, errors.Invalid(CodeInvalidInput,
			"at most %d coupon codes can be given in a single computation, %d given",
			MaxCodesPerCompute, len(codes))
	}

	out := make([]string, 0, len(codes))
	seen := make(map[string]struct{}, len(codes))
	for i, raw := range codes {
		code, err := normalizeCode(raw)
		if err != nil {
			return nil, withIndex(err, detailCodeIndex, i)
		}
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return out, nil
}

// validateLineID validates that a line identity is present and UNIQUE.
//
// Uniqueness is required: the result comes back keyed by line identity, and
// had the same identity appeared twice the caller could not tell which line
// received which discount.
func validateLineID(label, id string, seen map[string]struct{}) error {
	if err := validateText(label, id, 1, maxIDLen); err != nil {
		return err
	}
	if _, dup := seen[id]; dup {
		return errors.Invalid(CodeInvalidInput, "%s is repeated: %q", label, id)
	}
	seen[id] = struct{}{}
	return nil
}

// validateUnitAmount validates the item's unit price and its IDENTITY.
//
// The identity (unit × quantity = amount) is the one cross-field rule this
// contract carries, and the reason it is enforced here is that the two numbers
// can each be right SEPARATELY and wrong together: the reward computation
// reads from the unit, the line bound from the amount, and when the two
// diverge the promotion promises more than the line can carry.
//
// The product fits in an int64: the unit is at most [models.MaxAmount]
// (10^12) and the quantity at most [models.MaxQuantity] (10^6), so the
// intermediate result does not exceed 10^18.
func validateUnitAmount(item ComputeItem) error {
	if err := validateAmount("item unit price", item.UnitAmount); err != nil {
		return err
	}
	if item.UnitAmount*item.Quantity != item.Amount {
		return errors.Invalid(CodeInvalidInput,
			"the item amount has to be unit price × quantity: %d × %d = %d, %d given",
			item.UnitAmount, item.Quantity, item.UnitAmount*item.Quantity, item.Amount)
	}
	return nil
}

// lineState is the changing state of a single line over the course of a
// computation.
type lineState struct {
	// id is the line's identity.
	id string
	// amount is the line's ORIGINAL amount; it does not change over the
	// computation and is the base of the percentage discounts (the
	// non-compound decision).
	amount int64
	// unitAmount is the line's unit price; it is the base of the reward
	// computation, and for a shipping method it is the line amount itself (its
	// quantity is one).
	unitAmount int64
	// quantity is the line's quantity; for a shipping method it is one.
	quantity int64
	// attributes are the attributes the target rules look at.
	attributes map[string]string
	// lists are the attributes the target rule reads as a SET (ADR 0148).
	//
	// For a shipping method it is ALWAYS empty, and that is not a gap: a
	// shipping method is in no category and carries no tag, so the question
	// "in any of these categories" has no answer for shipping — the rule does
	// not match, which is the right answer.
	lists map[string][]string
	// discount is the TOTAL discount applied to the line so far.
	discount int64
}

// remaining returns how much more discount can be applied to the line.
func (l *lineState) remaining() int64 {
	if l.discount >= l.amount {
		return 0
	}
	return l.amount - l.discount
}

// charge applies a discount to the line and returns what was ACTUALLY applied.
//
// If the requested amount exceeds the line's remainder, it is clipped; the
// clipped part is lost and is not carried to another line (see
// [Service.ComputeDiscounts], "Upper bounds").
func (l *lineState) charge(want int64) int64 {
	if want <= 0 {
		return 0
	}
	if limit := l.remaining(); want > limit {
		want = limit
	}
	l.discount += want
	return want
}

// computeDiscounts produces the result from the candidates; it is a PURE
// function.
//
// It does not touch the database, the clock or logging. That is why every
// branch of the computation can be proven by a unit test without a database —
// for the discount arithmetic, the module's most critical decision, this is a
// requirement.
func computeDiscounts(candidates []models.PromotionCandidate, in ComputeInput) ComputeResult {
	items := make([]lineState, 0, len(in.Items))
	for i := range in.Items {
		items = append(items, lineState{
			id:         in.Items[i].ID,
			amount:     in.Items[i].Amount,
			unitAmount: in.Items[i].UnitAmount,
			quantity:   in.Items[i].Quantity,
			attributes: in.Items[i].Attributes,
			lists:      in.Items[i].Lists,
		})
	}
	shipping := make([]lineState, 0, len(in.ShippingMethods))
	for i := range in.ShippingMethods {
		shipping = append(shipping, lineState{
			id:         in.ShippingMethods[i].ID,
			amount:     in.ShippingMethods[i].Amount,
			unitAmount: in.ShippingMethods[i].Amount,
			quantity:   1,
			// A shipping method has no quantity; it counts as one unit in a
			// "fixed" + "each" discount.
			attributes: in.ShippingMethods[i].Attributes,
		})
	}

	eligible, skipped := partitionCandidates(candidates, in)
	applied := make([]AppliedPromotion, 0, len(eligible))
	for i := range eligible {
		amount := applyPromotion(eligible[i], items, shipping)
		if amount <= 0 {
			continue
		}
		applied = append(applied, AppliedPromotion{
			PromotionID: eligible[i].Promotion.ID,
			Code:        eligible[i].Promotion.Code,
			IsAutomatic: eligible[i].Promotion.IsAutomatic,
			Amount:      amount,
		})
	}

	result := ComputeResult{
		CurrencyCode:    in.CurrencyCode,
		Items:           make([]LineDiscount, 0, len(items)),
		ShippingMethods: make([]LineDiscount, 0, len(shipping)),
		Applied:         applied,
		Skipped:         skipped,
		UnmatchedCodes:  unmatchedCodes(in.Codes, eligible),
	}
	for i := range items {
		result.Items = append(result.Items, LineDiscount{ID: items[i].id, Amount: items[i].discount})
		result.ItemsDiscountTotal += items[i].discount
	}
	for i := range shipping {
		result.ShippingMethods = append(result.ShippingMethods,
			LineDiscount{ID: shipping[i].id, Amount: shipping[i].discount})
		result.ShippingDiscountTotal += shipping[i].discount
	}
	result.DiscountTotal = result.ItemsDiscountTotal + result.ShippingDiscountTotal
	return result
}

// partitionCandidates splits the candidates in two, the applicable ones and
// the ELIMINATED ones; the applicable ones are arranged in APPLICATION ORDER.
//
// The ordering rule is defined in the [Service.ComputeDiscounts] godoc: coupons
// first, then automatics; ascending by identity within each group.
//
// The eliminated ones come back IN CANDIDATE ORDER and are not sorted: which
// promotion was eliminated and why carries no application order, and putting
// an eliminated promotion "first" would attribute an application order to
// something that is not applied.
func partitionCandidates(
	candidates []models.PromotionCandidate, in ComputeInput,
) (eligible []models.PromotionCandidate, skipped []SkippedPromotion) {
	out := make([]models.PromotionCandidate, 0, len(candidates))
	skipped = make([]SkippedPromotion, 0)

	for i := range candidates {
		if reason := skipReasonOf(candidates[i], in); reason != "" {
			skipped = append(skipped, SkippedPromotion{
				PromotionID: candidates[i].Promotion.ID,
				Code:        candidates[i].Promotion.Code,
				Reason:      reason,
			})

			continue
		}
		out = append(out, candidates[i])
	}

	slices.SortFunc(out, func(a, b models.PromotionCandidate) int {
		// Coupons (the non-automatic ones) come first; an explicit criterion is
		// used instead of a bool ordering so that the intent is readable.
		if a.Promotion.IsAutomatic != b.Promotion.IsAutomatic {
			if a.Promotion.IsAutomatic {
				return 1
			}
			return -1
		}
		return cmp.Compare(a.Promotion.ID, b.Promotion.ID)
	})

	return out, skipped
}

// eligible reports whether a candidate can enter the computation (see
// "Elimination" in the [Service.ComputeDiscounts] godoc).
//
// [skipReasonOf] makes the decision and this function is its yes/no. Writing
// the elimination conditions in TWO places would have meant one silently
// drifting from the other: a promotion would be applied for one reason and
// reported eliminated for another.
func eligible(candidate models.PromotionCandidate, in ComputeInput) bool {
	return skipReasonOf(candidate, in) == ""
}

// campaignBudgetCurrencyMatches reports whether the campaign's MONEY-measured
// budget is in the same currency as the cart.
//
// Promotions with no campaign, with no budget, and with a QUANTITY-measured
// budget always pass: a budget that counts quantity has no currency and cannot
// be compared with the cart's.
//
// The check has to be HERE as well. At redemption time repository.Redeem
// enforces the same condition while the campaign row is locked and refuses a
// non-matching redemption with errors.Conflict. Had the elimination not been
// done in the computation, the customer would see the discount in the cart and
// get a 409 at order completion — or the saga would compensate the whole
// order. No currency conversion is made; the reasoning is in the
// [ComputeInput.CurrencyCode] godoc.
func campaignBudgetCurrencyMatches(candidate models.PromotionCandidate, currencyCode string) bool {
	campaign := candidate.Campaign
	if campaign == nil || campaign.BudgetType != models.BudgetSpend {
		return true
	}
	return campaign.BudgetCurrencyCode == currencyCode
}

// campaignUsable reports whether the candidate's campaign is fit to offer a
// discount at the given moment; a promotion with no campaign is always fit.
//
// If the campaign identity is set but there is no metadata, the campaign has
// been DELETED; the promotion is left without an owner and is not taken into
// the computation. Silently counting it as campaign-less would turn a discount
// that has a budget and dates into an unlimited one.
func campaignUsable(candidate models.PromotionCandidate, at time.Time) bool {
	if candidate.Promotion.CampaignID == nil {
		return true
	}
	if candidate.Campaign == nil {
		return false
	}
	return candidate.Campaign.WindowContains(at) && !candidate.Campaign.BudgetExhausted()
}

// unmatchedCodes returns the codes that could not be tied to an applicable
// promotion.
//
// A code counts as matched if the promotion that owns it PASSED THE
// ELIMINATION — even if it produced no discount. The distinction is
// deliberate: a valid coupon with no item matching its target is not an
// "invalid code"; it just was of no use in this cart.
func unmatchedCodes(codes []string, eligible []models.PromotionCandidate) []string {
	if len(codes) == 0 {
		return []string{}
	}

	matched := make(map[string]struct{}, len(eligible))
	for i := range eligible {
		matched[eligible[i].Promotion.Code] = struct{}{}
	}

	out := make([]string, 0, len(codes))
	for _, code := range codes {
		if _, ok := matched[code]; !ok {
			out = append(out, code)
		}
	}
	return out
}

// applyPromotion applies a single promotion to the lines and returns the total
// discount ACTUALLY applied.
//
// It modifies the line states IN PLACE; the returned value is the real total
// AFTER the part caught on the line bounds is SUBTRACTED.
func applyPromotion(candidate models.PromotionCandidate, items, shipping []lineState) int64 {
	method := candidate.Method
	targets := selectTargets(candidate, items, shipping)
	if len(targets) == 0 {
		return 0
	}

	// "Buy X, get Y" selects the targets the SAME way and gives the reward
	// counting UNITS; the allocation and the maximum quantity are not read
	// there, because the method's own pair of numbers says how many units to
	// discount.
	if candidate.Promotion.Type == models.PromotionBuyGet {
		return applyBuyGet(candidate, items, targets)
	}

	// An order target distributes a SINGLE total across the items; the
	// allocation is already forced to "across" at write time, and forcing it
	// here is a second defense against a hand-written record.
	allocation := method.Allocation
	if method.TargetType == models.TargetOrder {
		allocation = models.AllocationAcross
	}

	if allocation == models.AllocationEach {
		var applied int64
		for _, line := range targets {
			applied += line.charge(eachDiscount(*method, *line))
		}
		return applied
	}

	total := acrossTotal(*method, targets)
	lines := make([]allocLine, 0, len(targets))
	for _, line := range targets {
		lines = append(lines, allocLine{ID: line.id, Amount: line.amount})
	}

	var applied int64
	for i, share := range allocateAcross(total, lines) {
		applied += targets[i].charge(share)
	}
	return applied
}

// selectTargets selects the lines that will receive the promotion's discount.
//
// Target rules are applied ONLY to the "items" and "shipping_methods" targets.
// The "order" target discounts the WHOLE order; filtering a subset there would
// contradict the target's name — if a subset is wanted, the target has to be
// "items".
func selectTargets(candidate models.PromotionCandidate, items, shipping []lineState) []*lineState {
	switch candidate.Method.TargetType {
	case models.TargetItems:
		return filterLines(items, candidate.TargetRules())
	case models.TargetShippingMethods:
		return filterLines(shipping, candidate.TargetRules())
	case models.TargetOrder:
		return filterLines(items, nil)
	default:
		// An unrecognized target produces NO discount: a value that leaked into
		// the database later must not apply a discount to an arbitrary set of
		// lines.
		return nil
	}
}

// filterLines returns pointers to the lines that satisfy the target rules.
//
// A filter with no rules selects ALL lines. Returning pointers is deliberate:
// the discount is WRITTEN into the line's state, and working on a copy would
// lose the write.
func filterLines(lines []lineState, rules []models.PromotionRule) []*lineState {
	out := make([]*lineState, 0, len(lines))
	for i := range lines {
		// A line's LISTS arrived with ADR 0148: the product module publishes
		// membership from the Query layer, and the cart sends every line's
		// category and tag identities. On shipping lines the list is empty, and
		// its staying empty is correct (see [lineState.lists]).
		if len(rules) > 0 && !matchRules(rules, lines[i].attributes, lines[i].lists) {
			continue
		}
		out = append(out, &lines[i])
	}
	return out
}

// eachDiscount computes the RAW discount of a single line in an "each"
// allocation.
//
// Raw means not yet clipped to the line's remaining amount; the clipping is in
// [lineState.charge].
//
// For a fixed amount the discount is applied to EACH UNIT of the line, and
// [models.ApplicationMethod.MaxQuantity] bounds the number of units. For a
// percentage MaxQuantity IS IGNORED; the reasoning is in that field's godoc.
func eachDiscount(method models.ApplicationMethod, line lineState) int64 {
	switch method.Type {
	case models.MethodFixed:
		units := line.quantity
		if method.MaxQuantity != nil && *method.MaxQuantity < units {
			units = *method.MaxQuantity
		}
		if units <= 0 {
			return 0
		}
		return method.Value * units
	case models.MethodPercentage:
		return percentageOf(line.amount, method.Value)
	default:
		return 0
	}
}

// acrossTotal computes the TOTAL discount to distribute in an "across"
// allocation.
//
// The percentage is computed ONCE over the sum of the targets' amounts:
// computing it per line and adding up would round down by up to a cent on
// every line and give visibly less than the promotion promised.
//
// The base is the sum of the targets' ORIGINAL amounts — NOT their REMAINING
// amounts.
//
// The distinction is not a matter of a cent. Clipping to the remainder would
// penalize a line that an earlier promotion had filled TWICE: the pool to
// distribute would shrink by that line's remainder, but since
// [allocateAcross] still distributes the shares by the lines' ORIGINAL
// amounts, the full line keeps receiving its share, and that share is clipped
// inside [lineState.charge] and LOST. The empty line would be given half of
// what was promised, and that would turn the "percentages do NOT STACK on top
// of each other" decision in the [Service.ComputeDiscounts] godoc into a
// compound computation through the back door.
//
// The result is NOT clipped here AS WELL: the sole owner of the rule that an
// allocation cannot distribute more than the base it distributes over is
// [allocateAcross], and [lineState.charge] guards the line bound. Repeating
// the same clipping here would leave a line that changes no behavior — and
// that therefore no test could guard.
func acrossTotal(method models.ApplicationMethod, targets []*lineState) int64 {
	var base int64
	for _, line := range targets {
		base += line.amount
	}

	switch method.Type {
	case models.MethodFixed:
		return method.Value
	case models.MethodPercentage:
		return percentageOf(base, method.Value)
	default:
		return 0
	}
}

// percentageOf returns an amount's basis-point equivalent, rounding DOWN.
//
// The product fits in an int64: since the amount is at most
// [models.MaxAmount] (10^12) and the basis points at most
// [models.BasisPointDenominator] (10^4), the intermediate result does not
// exceed 10^16. The reasoning for the rounding direction is in the
// [models.BasisPointDenominator] godoc.
func percentageOf(amount, basisPoints int64) int64 {
	if amount <= 0 || basisPoints <= 0 {
		return 0
	}
	if amount > models.MaxAmount {
		amount = models.MaxAmount
	}
	if basisPoints > models.BasisPointDenominator {
		basisPoints = models.BasisPointDenominator
	}
	return amount * basisPoints / models.BasisPointDenominator
}

// The index keys in an error's details.
const (
	// detailItemIndex reports which ITEM was rejected.
	detailItemIndex = "item_index"
	// detailShippingIndex reports which SHIPPING METHOD was rejected.
	detailShippingIndex = "shipping_index"
	// detailCodeIndex reports which COUPON CODE was rejected.
	detailCodeIndex = "code_index"
)

// withIndex adds to a validation error which input it occurred at.
//
// In a batch computation, knowing which line was rejected is the one piece of
// information that makes the error usable. The key comes from the caller
// because the item, shipping and code lists are SEPARATE, and a single "index"
// key would not say which one was meant.
func withIndex(err error, key string, index int) error {
	var typed *errors.Error
	if errors.As(err, &typed) && typed != nil {
		return typed.WithDetails(map[string]any{key: index})
	}
	return err
}

// storeCouponVisible reports whether a promotion can be shown to the CUSTOMER.
//
// This is how it differs from the admin surface: draft and inactive
// promotions, campaigns whose window has closed or whose budget is exhausted,
// and coupons whose usage allowance has run out all look "nonexistent" to the
// customer.
//
// The mechanic AGREEING with the method is also checked here, using the same
// predicate as the computation's elimination ([mechanicMatchesMethod]). Had
// they been written separately, one would offer the coupon to the customer
// while the other eliminated it: the customer types the code, nothing happens,
// and no reason stands anywhere.
//
// Also, RULE CONDITIONS never go out: a rule's right-hand side (e.g. a customer
// group's identity) is business information. That is also why the rules are
// NOT EVALUATED here: they cannot be evaluated without a cart context, and the
// answer "you do not satisfy its conditions" would give away that the
// condition exists. Whether the coupon really produces a discount in that cart
// is told by [Service.ComputeDiscounts].
func storeCouponVisible(candidate models.PromotionCandidate, at time.Time) bool {
	promo := candidate.Promotion
	if promo.Status != models.PromotionActive || !promo.Type.Valid() {
		return false
	}
	if !mechanicMatchesMethod(promo, candidate.Method) {
		return false
	}
	if promo.UsageExhausted() {
		return false
	}
	return campaignUsable(candidate, at)
}

// StoreCoupon is the coupon information that can be shown to the customer.
//
// It is deliberately NARROW: status, usage counter, campaign budget, metadata
// and rule conditions are NOT IN IT. The only thing the customer needs to see
// is that the coupon is valid and what kind of discount it gives.
type StoreCoupon struct {
	// Code is the coupon code (UPPER case).
	Code string
	// Mechanic is the promotion's mechanic (standard | buyget).
	//
	// It HAS TO STAND beside the measure: a "buy 2, get one" coupon carries ten
	// thousand basis points, and had the mechanic not been stated the
	// storefront would show it as "100% off" — something other than what the
	// coupon gives.
	Mechanic models.PromotionType
	// BuyQuantity is the quantity that has to be bought to earn the reward; it
	// is set only on a "buyget" coupon.
	BuyQuantity *int64
	// ApplyToQuantity is the quantity the reward applies to; it is set only on
	// a "buyget" coupon.
	//
	// Neither is a CONDITION; both are the OFFER: the rules' right-hand side
	// still does not go out, what goes out is what the coupon gives.
	ApplyToQuantity *int64
	// MethodType is the discount's measure (fixed | percentage).
	MethodType models.ApplicationMethodType
	// TargetType is the discount's target (items | shipping_methods | order).
	TargetType models.ApplicationTargetType
	// Value is a fixed amount (minor unit) or basis points.
	Value int64
	// CurrencyCode is the currency of a fixed-amount discount; for a
	// percentage it is empty.
	CurrencyCode string
}

// LookupStoreCoupon returns the information about a coupon code that can be
// shown to the CUSTOMER.
//
// If the code does not exist, the promotion is draft/inactive, its campaign's
// window is closed, its budget is exhausted or its usage allowance has run
// out, the SAME error is returned: errors.NotFound (code:
// [CodePromotionNotUsable]). Making no distinction is deliberate — the answer
// "this code exists but its campaign has not started yet" would give away the
// existence of an unpublished campaign and would let someone guessing codes
// work out a campaign calendar.
func (s *Service) LookupStoreCoupon(ctx context.Context, code string) (StoreCoupon, error) {
	if err := s.ready(); err != nil {
		return StoreCoupon{}, err
	}
	normalized, err := normalizeCode(code)
	if err != nil {
		// A code that is invalid in form also counts as "nonexistent": an error
		// validating the code's form would help narrow the search space by
		// trying valid forms.
		return StoreCoupon{}, notUsable(code)
	}

	candidate, err := s.storeCandidate(ctx, normalized)
	if err != nil {
		if errors.IsNotFound(err) {
			return StoreCoupon{}, notUsable(normalized)
		}
		// An infrastructure failure is not turned into "no coupon": showing the
		// customer a temporary error as if it were a permanent answer would be
		// silently refusing a valid coupon.
		return StoreCoupon{}, err
	}

	// The method's presence is also part of the visibility decision; a
	// separate check would be answering the same question from two places.
	if !storeCouponVisible(candidate, s.clock()) {
		return StoreCoupon{}, notUsable(normalized)
	}

	return StoreCoupon{
		Code:            candidate.Promotion.Code,
		Mechanic:        candidate.Promotion.Type,
		BuyQuantity:     candidate.Method.BuyQuantity,
		ApplyToQuantity: candidate.Method.ApplyToQuantity,
		MethodType:      candidate.Method.Type,
		TargetType:      candidate.Method.TargetType,
		Value:           candidate.Method.Value,
		CurrencyCode:    candidate.Method.CurrencyCode,
	}, nil
}

// storeCandidate reads the candidate for coupon validation WITHOUT A FILTER.
//
// The candidate list [Service.ComputeDiscounts] uses returns only ACTIVE
// promotions. Had this surface read from it, the "what is visible to the
// customer" rule would be split in TWO places — one [storeCouponVisible], the
// other that query's WHERE — and the check here would silently stay dead: a
// change removing the status filter would fail no test.
//
// That is why the candidate is built without a filter and [storeCouponVisible]
// becomes the SOLE owner of the visibility decision. The cost is three
// queries; this is a SINGLE action the customer takes when typing a coupon, and
// it does not run on every round like the cart computation.
//
// If the application method or the campaign is not found, a missing field is
// returned, NOT an error: both mean "the coupon cannot be used", and
// [storeCouponVisible] makes the decision.
func (s *Service) storeCandidate(ctx context.Context, code string) (models.PromotionCandidate, error) {
	promo, err := s.repo.GetPromotionByCode(ctx, code)
	if err != nil {
		return models.PromotionCandidate{}, err
	}
	candidate := models.PromotionCandidate{Promotion: promo}

	method, err := s.repo.GetApplicationMethod(ctx, promo.ID)
	switch {
	case err == nil:
		candidate.Method = &method
	case !errors.IsNotFound(err):
		return models.PromotionCandidate{}, err
	}

	if promo.CampaignID != nil {
		campaign, campErr := s.repo.GetCampaign(ctx, *promo.CampaignID)
		switch {
		case campErr == nil:
			candidate.Campaign = &campaign
		case !errors.IsNotFound(campErr):
			return models.PromotionCandidate{}, campErr
		}
	}
	return candidate, nil
}

// notUsable produces the single-shaped "no coupon" error returned to the
// customer.
//
// The message repeats the code itself but says NOTHING else; the reasoning for
// its making no distinction is in the [Service.LookupStoreCoupon] godoc.
func notUsable(code string) error {
	return errors.NotFound(CodePromotionNotUsable,
		"coupon is not usable: %s", strings.TrimSpace(code))
}

// cloneLists produces a DEEP copy of the context lists.
//
// maps.Clone is not enough: the values are slices, and a shallow copy would
// share the caller's slice — something modifying the normalized input would
// modify the caller's data too. The reason this file calls maps.Clone for
// Context is the same boundary; there the values are strings, so it suffices.
func cloneLists(in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}

	out := make(map[string][]string, len(in))
	for key, values := range in {
		out[key] = slices.Clone(values)
	}

	return out
}
