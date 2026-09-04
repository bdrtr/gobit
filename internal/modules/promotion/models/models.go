// Package models defines the domain models of the promotion module.
//
// The types here are STRIPPED of database types: pgtype does not enter this package,
// the conversion is done in the repository wrapper. The service and API layers are
// thereby not bound to a storage detail.
//
// Money is always an INTEGER minor unit (cents) and the currency lives in a separate
// field (plan Section 8); float is used nowhere. RATES are basis points (2000 = 20%)
// and the rounding direction is documented next to [BasisPointDenominator]. Times
// are UTC.
package models

import "time"

// Amount, rate and quantity limits.
//
// The limits are not arbitrary, they are OVERFLOW protection. The largest
// intermediate product is MaxAmount × BasisPointDenominator = 10^12 × 10^4 = 10^16,
// which is far below int64's upper bound of 9.22×10^18; a percentage calculation
// therefore cannot overflow structurally. The same limits are repeated in the CHECK
// constraints of the migration: even if the service validation is skipped, the
// database is the second gate.
const (
	// MinAmount is the smallest permitted amount.
	MinAmount int64 = 0
	// MaxAmount is the largest permitted amount (minor unit). Both the amount of a
	// single line and the subtotal of a computation are bound by this limit.
	MaxAmount int64 = 1_000_000_000_000
	// MinQuantity is the smallest quantity of a line.
	MinQuantity int64 = 1
	// MaxQuantity is the largest quantity of a line.
	MaxQuantity int64 = 1_000_000
	// BasisPointDenominator is the basis point denominator: 10000 basis points = 100%.
	//
	// # Rounding direction
	//
	// A percentage discount is computed as `amount * bps / BasisPointDenominator`
	// with INTEGER arithmetic, and because Go's integer division truncates toward
	// zero the result is rounded DOWN. The direction is deliberate:
	//
	//   - The discount never EXCEEDS the promised percentage. Rounding up would mean
	//     that a campaign advertising "20% off" gives more than 20% at the cent
	//     level, and the campaign budget would not stay bounded by exactly what was
	//     promised.
	//   - The error is at most ONE minor unit per line.
	//   - In an "across" allocation the total is rounded ONCE and the cent remainder
	//     is distributed over the lines (see the allocation rule in the service
	//     package), so the loss at cart level is again at most one minor unit.
	BasisPointDenominator int64 = 10_000
)

// CampaignBudgetType is the unit of measure of a campaign budget.
type CampaignBudgetType string

// Campaign budget types.
const (
	// BudgetNone is a campaign without a budget; usage is unlimited.
	BudgetNone CampaignBudgetType = "none"
	// BudgetSpend measures the budget as MONEY; every redemption consumes as much
	// budget as the discount amount it used, and the budget currency is mandatory.
	BudgetSpend CampaignBudgetType = "spend"
	// BudgetUsage measures the budget as a COUNT; every redemption consumes one.
	BudgetUsage CampaignBudgetType = "usage"
)

// Valid reports whether the type is defined.
func (t CampaignBudgetType) Valid() bool {
	return t == BudgetNone || t == BudgetSpend || t == BudgetUsage
}

// Campaign is the container of promotions: it carries a shared date window and a
// shared budget.
//
// The container does not stand IN PLACE OF a promotion's own status: a promotion is
// applied when both its own status and its campaign's window/budget are eligible.
type Campaign struct {
	// ID is the "camp_" prefixed, time-ordered identifier.
	ID string
	// Name is the display name of the campaign; it cannot be empty.
	Name string
	// CampaignIdentifier is the UNIQUE business identifier given by the operator
	// (e.g. "BLACKFRIDAY-2026"). It is separate from the ID: outside systems know the
	// campaign by this name, and the name, unlike the ID, has to be readable.
	CampaignIdentifier string
	// Description is the optional description.
	Description string
	// StartsAt is the start of the validity window; if nil there is no lower bound.
	StartsAt *time.Time
	// EndsAt is the end of the validity window; if nil there is no upper bound.
	EndsAt *time.Time
	// BudgetType is the unit of measure of the budget.
	BudgetType CampaignBudgetType
	// BudgetLimit is the upper bound of the budget; if nil there is no bound. Its
	// unit is the minor unit or a count, according to [BudgetType].
	BudgetLimit *int64
	// BudgetUsed is the CONSUMED part of the budget; it is in the same unit as
	// [BudgetLimit].
	BudgetUsed int64
	// BudgetCurrencyCode is the currency of a "spend" budget (ISO 4217, UPPERCASE);
	// on the other types it is empty.
	BudgetCurrencyCode string
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the moment of the soft delete; if nil the record is live.
	DeletedAt *time.Time
}

// WindowContains reports whether the given moment falls inside the campaign's window.
//
// The ends of the window are INCLUSIVE (a nil end = unbounded): a campaign still
// being valid at its ending moment is preferable to a second-precision boundary
// telling the customer "the campaign is over".
func (c Campaign) WindowContains(at time.Time) bool {
	if c.StartsAt != nil && at.Before(*c.StartsAt) {
		return false
	}
	if c.EndsAt != nil && at.After(*c.EndsAt) {
		return false
	}
	return true
}

// BudgetExhausted reports whether the budget is EXHAUSTED.
//
// An unbounded budget (nil limit, or type "none") is never exhausted. If there is a
// bound, the exhaustion condition is `used >= limit`: on a budget that sits EXACTLY
// on the bound, a new redemption would exceed it.
func (c Campaign) BudgetExhausted() bool {
	if c.BudgetType == BudgetNone || c.BudgetLimit == nil {
		return false
	}
	return c.BudgetUsed >= *c.BudgetLimit
}

// BudgetDeltaFor reports how much a redemption will consume from the budget.
//
// The unit depends on [BudgetType]: a "spend" budget consumes MONEY (as much as the
// applied discount amount), a "usage" budget consumes a COUNT (one per redemption),
// a campaign without a budget consumes nothing.
//
// The rule lives here, in the domain; the redemption flow only CALLS it. Had it been
// copied into the repository layer, the two places drifting apart when a budget type
// is added would be a silent accounting error.
func (c Campaign) BudgetDeltaFor(amount int64) int64 {
	switch c.BudgetType {
	case BudgetSpend:
		return amount
	case BudgetUsage:
		return 1
	case BudgetNone:
		return 0
	default:
		// An unrecognized type CONSUMES NO budget. The alternative (deducting the
		// amount) would corrupt a counter in an unknown unit; not consuming leads not
		// to the budget being overspent but only to it being undercounted, and the
		// situation stays visible on the admin surface.
		return 0
	}
}

// PromotionType is the mechanic of a promotion.
type PromotionType string

// Promotion types.
const (
	// PromotionStandard is the promotion that applies a discount directly.
	PromotionStandard PromotionType = "standard"
	// PromotionBuyGet is the "buy N pay M" mechanic.
	//
	// # It CANNOT BE ACTIVATED in this phase
	//
	// The mechanic requires answering "which lines satisfy the BUY condition" and "on
	// how many UNITS of which lines will the discount be applied"; the second one
	// asks for the line's UNIT price, and the line amount (unit × quantity) carried
	// by the service's computation input
	// ([github.com/bdrtr/gobit/internal/modules/promotion/service.ComputeInput])
	// cannot be turned into a unit price without dividing — and the division would
	// produce a silent rounding error on a line that does not divide evenly by the
	// quantity.
	//
	// So as not to leave the gap SILENT, the type is closed STRUCTURALLY: a buyget
	// promotion can be created but cannot be moved into the "active" status (see the
	// service validation), and the computation skips it as a safety net as well. That
	// way the state "an active promotion that is set up but does nothing" cannot
	// arise.
	PromotionBuyGet PromotionType = "buyget"
)

// Valid reports whether the type is defined.
func (t PromotionType) Valid() bool {
	return t == PromotionStandard || t == PromotionBuyGet
}

// PromotionStatus is the publication status of a promotion.
type PromotionStatus string

// Promotion statuses.
const (
	// PromotionDraft is a promotion not yet published; it DOES NOT take part in the
	// computation and is NOT VISIBLE on the customer surface.
	PromotionDraft PromotionStatus = "draft"
	// PromotionActive is a published promotion.
	PromotionActive PromotionStatus = "active"
	// PromotionInactive is a promotion stopped by hand; it DOES NOT take part in the
	// computation.
	PromotionInactive PromotionStatus = "inactive"
)

// Valid reports whether the status is defined.
func (s PromotionStatus) Valid() bool {
	return s == PromotionDraft || s == PromotionActive || s == PromotionInactive
}

// Promotion is a single discount definition.
//
// It is applied either with a coupon code ([Code]) or without one ([IsAutomatic]).
// Both can hold at once: an automatic promotion has a code too, because the code is
// at the same time the name by which the operator refers to the promotion, and it is
// unique.
type Promotion struct {
	// ID is the "promo_" prefixed identifier.
	ID string
	// Code is the coupon code; it is UNIQUE and is always stored in UPPERCASE.
	Code string
	// IsAutomatic reports whether the promotion is applied without a code being
	// entered.
	IsAutomatic bool
	// Type is the mechanic of the promotion.
	Type PromotionType
	// CampaignID binds the promotion to a campaign; if nil the promotion has no
	// campaign and is bounded only by its own rules.
	CampaignID *string
	// Status is the publication status of the promotion.
	Status PromotionStatus
	// UsageLimit is how many times the promotion can be used; if nil it is unbounded.
	UsageLimit *int64
	// UsageCount is how many times the promotion HAS BEEN USED (see [Redemption]).
	UsageCount int64
	// Metadata is the operator's free key/value note; it does not enter business
	// rules.
	Metadata map[string]string
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the moment of the soft delete; if nil the record is live.
	DeletedAt *time.Time
}

// UsageExhausted reports whether the usage limit is used up.
//
// An unbounded promotion is never exhausted. If there is a bound, the exhaustion
// condition is `used >= limit`.
func (p Promotion) UsageExhausted() bool {
	if p.UsageLimit == nil {
		return false
	}
	return p.UsageCount >= *p.UsageLimit
}

// ApplicationMethodType reports how the discount is measured.
type ApplicationMethodType string

// Application method types.
const (
	// MethodFixed is a FIXED AMOUNT discount; its value is a minor unit and the
	// currency is MANDATORY.
	MethodFixed ApplicationMethodType = "fixed"
	// MethodPercentage is a PERCENTAGE discount; its value is in BASIS POINTS
	// (2000 = 20%) and it carries no currency.
	MethodPercentage ApplicationMethodType = "percentage"
)

// Valid reports whether the type is defined.
func (t ApplicationMethodType) Valid() bool {
	return t == MethodFixed || t == MethodPercentage
}

// ApplicationTargetType reports WHAT the discount will be applied to.
type ApplicationTargetType string

// Application targets.
const (
	// TargetItems applies the discount to the cart LINES.
	TargetItems ApplicationTargetType = "items"
	// TargetShippingMethods applies the discount to the SHIPPING methods.
	TargetShippingMethods ApplicationTargetType = "shipping_methods"
	// TargetOrder applies the discount to the whole ORDER; the result is again
	// allocated to the lines (see the allocation rule in the service package),
	// because the cart total expects a discount per line.
	TargetOrder ApplicationTargetType = "order"
)

// Valid reports whether the target is defined.
func (t ApplicationTargetType) Valid() bool {
	return t == TargetItems || t == TargetShippingMethods || t == TargetOrder
}

// Allocation reports how the discount will be distributed among the target lines.
type Allocation string

// Allocation forms.
const (
	// AllocationEach applies the discount to EVERY target line SEPARATELY: a
	// percentage works on each line's own amount, a fixed amount works on every unit
	// of every line.
	AllocationEach Allocation = "each"
	// AllocationAcross computes the discount as a SINGLE amount and distributes it
	// over the target lines PROPORTIONALLY to their amounts.
	AllocationAcross Allocation = "across"
)

// Valid reports whether the allocation form is defined.
func (a Allocation) Valid() bool {
	return a == AllocationEach || a == AllocationAcross
}

// ApplicationMethod is HOW a promotion will apply its discount.
//
// A promotion has AT MOST ONE application method; a promotion without a method
// produces no discount and is skipped in the computation.
type ApplicationMethod struct {
	// ID is the "appm_" prefixed identifier.
	ID string
	// PromotionID is the promotion the method is bound to.
	PromotionID string
	// Type is the measure of the discount (fixed | percentage).
	Type ApplicationMethodType
	// TargetType is the target of the discount (items | shipping_methods | order).
	TargetType ApplicationTargetType
	// Allocation is the distribution form (each | across).
	Allocation Allocation
	// Value is the fixed amount (minor unit) or the basis points (according to
	// [Type]).
	Value int64
	// MaxQuantity is the maximum QUANTITY the fixed amount will be applied to, and it
	// is meaningful ONLY in the "fixed" + "each" combination; if nil there is no
	// bound.
	//
	// In the other combinations it is IGNORED. The reason: a percentage discount
	// works on the line's AMOUNT, and bounding the quantity would require dividing
	// the line amount by the unit price — on a line that does not divide evenly by
	// the quantity that would be a silent rounding error. In "across" there is a
	// single distributed total anyway and the notion of quantity is already out of
	// play.
	MaxQuantity *int64
	// CurrencyCode is the currency of a "fixed" discount (ISO 4217, UPPERCASE); on
	// "percentage" it is empty.
	CurrencyCode string
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
}

// RuleOperator is the comparison operator of a promotion rule.
type RuleOperator string

// Supported operators.
//
// eq/ne/in/nin are STRING comparisons; gt/gte/lt/lte convert both sides to integers
// and compare NUMERICALLY. A context value that cannot be converted to a number makes
// the rule NOT MATCH, it does not produce an error: the context comes from outside
// and a single broken field must not bring down the whole discount computation.
//
// It is the SAME concept as PriceRule in the pricing module. That package CANNOT be
// imported (Principle 2.4 / ADR 0001) and the type is redefined here; this is the
// price of isolation, explicitly accepted in ADR 0001.
const (
	// OpEq wants the value to equal the rule's single value.
	OpEq RuleOperator = "eq"
	// OpNe wants the value to differ from the rule's single value.
	OpNe RuleOperator = "ne"
	// OpIn wants the value to be present in the rule's set.
	OpIn RuleOperator = "in"
	// OpNin wants the value to be ABSENT from the rule's set.
	OpNin RuleOperator = "nin"
	// OpGt wants numerical greater-than.
	OpGt RuleOperator = "gt"
	// OpGte wants numerical greater-than-or-equal.
	OpGte RuleOperator = "gte"
	// OpLt wants numerical less-than.
	OpLt RuleOperator = "lt"
	// OpLte wants numerical less-than-or-equal.
	OpLte RuleOperator = "lte"
)

// Valid reports whether the operator is defined.
func (o RuleOperator) Valid() bool {
	switch o {
	case OpEq, OpNe, OpIn, OpNin, OpGt, OpGte, OpLt, OpLte:
		return true
	default:
		return false
	}
}

// Numeric reports whether the operator performs a numerical comparison.
func (o RuleOperator) Numeric() bool {
	switch o {
	case OpGt, OpGte, OpLt, OpLte:
		return true
	case OpEq, OpNe, OpIn, OpNin:
		return false
	default:
		return false
	}
}

// MultiValue reports whether the operator can take more than one value.
// Every other operator wants a SINGLE value.
func (o RuleOperator) MultiValue() bool {
	return o == OpIn || o == OpNin
}

// RuleType reports WHAT a rule will look at.
type RuleType string

// Rule types.
const (
	// RuleContext looks at the CART CONTEXT (currency, region, customer group …).
	// If a context rule is not satisfied, the whole promotion is not applied.
	RuleContext RuleType = "context"
	// RuleTarget looks at LINE attributes and filters which lines the discount will
	// land on. It is meaningful on promotions whose target is "items" or "order"; on
	// a shipping target the attributes of the shipping method are filtered.
	RuleTarget RuleType = "target"
)

// Valid reports whether the rule type is defined.
func (t RuleType) Valid() bool {
	return t == RuleContext || t == RuleTarget
}

// PromotionRule is a condition for a promotion to be applied.
//
// Example: {RuleType: RuleContext, Attribute: "customer_group_id",
// Operator: OpIn, Values: []string{"vip", "b2b"}}.
type PromotionRule struct {
	// ID is the "prule_" prefixed identifier.
	ID string
	// PromotionID is the promotion the rule is bound to.
	PromotionID string
	// RuleType is what the rule looks at.
	RuleType RuleType
	// Attribute is the name of the field to look at, in the context or on the line.
	Attribute string
	// Operator is the comparison operator.
	Operator RuleOperator
	// Values is the right-hand side of the comparison; it holds at least one element.
	Values []string
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
}

// Redemption is the record that a promotion was used for ONE single reference.
//
// The counter itself is the [Promotion.UsageCount] and [Campaign.BudgetUsed] columns;
// this record is their LEDGER and it makes two things possible:
//
//   - Idempotency: a second redemption for the same reference does not increment the
//     counter a second time (see RedeemPromotion in the service layer).
//   - Reversal: the release does not GUESS how much was added to the counter; the
//     added value is kept in the [BudgetDelta] field and is deducted verbatim. The
//     ledger stays consistent even if the type of the campaign budget changed in the
//     meantime.
type Redemption struct {
	// ID is the "predeem_" prefixed identifier.
	ID string
	// PromotionID is the promotion that was used.
	PromotionID string
	// CampaignID is the campaign the promotion was bound to at the moment of use; if
	// nil the promotion had no campaign.
	CampaignID *string
	// Reference is the business record the use belongs to (e.g. an order id). It is
	// FREE text and is NOT a foreign key (Principle 2.2).
	Reference string
	// Amount is the discount amount actually applied in the use (minor unit).
	Amount int64
	// CurrencyCode is the currency of the discount (ISO 4217, UPPERCASE).
	CurrencyCode string
	// BudgetDelta is the value ADDED to the campaign budget; on release it is
	// deducted verbatim. On a promotion without a campaign or without a budget it is
	// zero.
	BudgetDelta int64
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
	// ReleasedAt is the moment of release; if nil the use is still in force.
	ReleasedAt *time.Time
}

// Released reports whether the use has been released.
func (r Redemption) Released() bool { return r.ReleasedAt != nil }

// PromotionCandidate is a single promotion entering the computation, together with
// its context.
//
// The promotion by itself is not enough: the measure of the discount is in
// [ApplicationMethod], the conditions for applying it are in [PromotionRule], and the
// date window and the budget are in [Campaign]. All four are carried TOGETHER so that
// the computation does not have to issue an extra query per candidate (there is no
// N+1).
type PromotionCandidate struct {
	// Promotion is the promotion itself.
	Promotion Promotion
	// Campaign is the promotion's campaign; if nil the promotion has no campaign.
	//
	// If Promotion.CampaignID is set but this field is nil, the campaign has been
	// DELETED and the promotion does not enter the computation (see the elimination
	// rule in the service layer).
	Campaign *Campaign
	// Method is the application method of the discount; if nil the promotion produces
	// no discount.
	Method *ApplicationMethod
	// Rules are ALL the rules of the promotion (context and target together).
	Rules []PromotionRule
}

// ContextRules returns the CONTEXT rules of the candidate.
func (c PromotionCandidate) ContextRules() []PromotionRule {
	return c.rulesOfType(RuleContext)
}

// TargetRules returns the TARGET rules of the candidate.
func (c PromotionCandidate) TargetRules() []PromotionRule {
	return c.rulesOfType(RuleTarget)
}

// rulesOfType filters the rules of the given type.
func (c PromotionCandidate) rulesOfType(t RuleType) []PromotionRule {
	out := make([]PromotionRule, 0, len(c.Rules))
	for i := range c.Rules {
		if c.Rules[i].RuleType == t {
			out = append(out, c.Rules[i])
		}
	}
	return out
}
