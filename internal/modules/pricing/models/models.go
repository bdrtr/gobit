// Package models defines the domain models of the pricing module.
//
// The types here are STRIPPED of database types: pgtype does not enter this
// package, the conversion is done in the repository wrapper. The service and API
// layers are therefore not bound to a storage detail.
//
// Money is ALWAYS an INTEGER minor unit (cents) and the currency lives in a
// separate field (plan Section 8); float is used nowhere. Times are UTC.
package models

import "time"

// Amount and quantity limits.
//
// The limits are not arbitrary: in Phase 5 the cart total will be computed as
// amount × quantity and that product MUST FIT in an int64. Because MaxAmount ×
// MaxQuantity = 10^12 × 10^6 = 10^18 < 9.22×10^18, overflow is structurally
// impossible. The same limits are repeated in the migration's CHECK constraints
// as well; even if service validation is skipped, the database is the second
// gate.
const (
	// MinAmount is the smallest permitted amount. A negative price is not a
	// discount; discounts are the promotion module's job.
	MinAmount int64 = 0
	// MaxAmount is the largest permitted amount (minor unit).
	MaxAmount int64 = 1_000_000_000_000
	// MinQuantity is the smallest value the lower bound of a price range may take.
	MinQuantity int32 = 1
	// MaxQuantity is the largest value the upper bound of a price range may take.
	MaxQuantity int32 = 1_000_000
)

// PriceSet is the container of a variant's prices.
//
// The container does NOT KNOW which variant it belongs to: the bond is
// established through the "product_variant_price_set" link the product module
// declares, and pricing never sees that link (Principle 2.1/2.4). pricing only
// produces the container and returns its id.
type PriceSet struct {
	// ID is the "pset_" prefixed, time-ordered id.
	ID string
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the soft delete moment; if nil the record is live.
	DeletedAt *time.Time
}

// Price is the amount valid for a single currency and quantity range.
type Price struct {
	// ID is the "price_" prefixed id.
	ID string
	// PriceSetID is the id of the container the price belongs to.
	PriceSetID string
	// PriceListID binds the price to a campaign/segment list; if nil this is a
	// BASE price.
	PriceListID *string
	// CurrencyCode is the ISO 4217 currency code; it is always stored UPPERCASE.
	CurrencyCode string
	// Amount is the amount in minor units (cents).
	Amount int64
	// MinQuantity is the smallest quantity the price is valid for (at least 1).
	MinQuantity int32
	// MaxQuantity is the largest quantity the price is valid for; if nil there is
	// no upper bound.
	MaxQuantity *int32
	// Rules are the price's validity conditions; if empty the price is
	// unconditional.
	Rules []PriceRule
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
}

// RuleOperator is the comparison operator of a price rule.
type RuleOperator string

// Supported operators.
//
// eq/ne/in/nin are STRING comparisons; gt/gte/lt/lte convert both sides to
// integers and compare NUMERICALLY (e.g. "customer_age" > "18"). A context value
// that cannot be converted to a number makes the rule NOT MATCH, it does not
// produce an error: the context comes from outside and a single broken field
// must not bring down the whole price calculation.
const (
	// OpEq requires the value to equal the rule's single value.
	OpEq RuleOperator = "eq"
	// OpNe requires the value to differ from the rule's single value.
	OpNe RuleOperator = "ne"
	// OpIn requires the value to be present in the rule's set.
	OpIn RuleOperator = "in"
	// OpNin requires the value to be ABSENT from the rule's set.
	OpNin RuleOperator = "nin"
	// OpGt requires numeric greater-than.
	OpGt RuleOperator = "gt"
	// OpGte requires numeric greater-than-or-equal.
	OpGte RuleOperator = "gte"
	// OpLt requires numeric less-than.
	OpLt RuleOperator = "lt"
	// OpLte requires numeric less-than-or-equal.
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

// Numeric reports whether the operator performs a numeric comparison.
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
// Every other operator requires a SINGLE value.
func (o RuleOperator) MultiValue() bool {
	return o == OpIn || o == OpNin
}

// PriceRule is the rule stating under which condition a price is valid.
//
// Example: {Attribute: "region_id", Operator: OpEq, Values: []string{"reg_1"}}.
type PriceRule struct {
	// ID is the "prule_" prefixed id.
	ID string
	// PriceID is the price the rule is attached to.
	PriceID string
	// Attribute is the name of the field to look at in the calculation context.
	Attribute string
	// Operator is the comparison operator.
	Operator RuleOperator
	// Values is the right-hand side of the comparison; it holds at least one
	// element.
	Values []string
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
}

// PriceListType is the type of a price list.
type PriceListType string

// Price list types.
const (
	// PriceListSale is the campaign (discount) list.
	PriceListSale PriceListType = "sale"
	// PriceListOverride is the list that takes the place of the base price;
	// contractual/B2B pricing is of this type and it overrides the campaign too.
	PriceListOverride PriceListType = "override"
)

// Valid reports whether the type is defined.
func (t PriceListType) Valid() bool {
	return t == PriceListSale || t == PriceListOverride
}

// Priority is the selection priority of the type; the larger value comes first.
//
// The base price (with no list) has priority 0; the zero value therefore means
// BASE and an undefined type cannot accidentally get ahead of a campaign.
func (t PriceListType) Priority() int {
	switch t {
	case PriceListOverride:
		return 2
	case PriceListSale:
		return 1
	default:
		return 0
	}
}

// PriceListStatus is the status of a price list.
type PriceListStatus string

// Price list statuses.
const (
	// PriceListDraft is a list not yet published; its prices are NOT INCLUDED in
	// the calculation.
	PriceListDraft PriceListStatus = "draft"
	// PriceListActive is the published list.
	PriceListActive PriceListStatus = "active"
	// PriceListExpired is a list ended by hand; its prices are NOT INCLUDED in
	// the calculation.
	PriceListExpired PriceListStatus = "expired"
)

// Valid reports whether the status is defined.
func (s PriceListStatus) Valid() bool {
	return s == PriceListDraft || s == PriceListActive || s == PriceListExpired
}

// PriceList is the campaign/segment price list.
type PriceList struct {
	// ID is the "plist_" prefixed id.
	ID string
	// Title is the list's display name; it cannot be empty.
	Title string
	// Description is the optional description.
	Description string
	// Type is the list's type (sale | override).
	Type PriceListType
	// Status is the list's status (draft | active | expired).
	Status PriceListStatus
	// StartsAt is the start of the validity window; if nil there is no lower
	// bound.
	StartsAt *time.Time
	// EndsAt is the end of the validity window; if nil there is no upper bound.
	EndsAt *time.Time
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
}

// PriceListInfo is the metadata of the list bound to a price, as used in the
// calculation.
//
// This narrow view is carried instead of the full [PriceList]: the calculation
// does not use the title and the description, and carrying them would make them
// look like inputs of the calculation.
type PriceListInfo struct {
	// ID is the list's id.
	ID string
	// Type is the list's type.
	Type PriceListType
	// Status is the list's status.
	Status PriceListStatus
	// StartsAt is the start of the validity window; if nil there is no lower
	// bound.
	StartsAt *time.Time
	// EndsAt is the end of the validity window; if nil there is no upper bound.
	EndsAt *time.Time
}

// Usable reports whether the list is fit to offer a price at the given moment.
//
// Fitness means two conditions holding TOGETHER: the status must be active and
// the moment must fall inside the [StartsAt, EndsAt] window. The window's ends
// are inclusive (a nil end = unbounded).
func (l PriceListInfo) Usable(at time.Time) bool {
	if l.Status != PriceListActive {
		return false
	}
	if l.StartsAt != nil && at.Before(*l.StartsAt) {
		return false
	}
	if l.EndsAt != nil && at.After(*l.EndsAt) {
		return false
	}
	return true
}

// PriceCandidate is a single price entering the calculation together with its
// list (if any).
type PriceCandidate struct {
	// Price is the price itself (its rules included).
	Price Price
	// List is the metadata of the list the price is bound to; if nil this is a
	// base price.
	//
	// If PriceListID is set but List is nil, the price's list has been DELETED
	// and the price is not included in the calculation (see the selection rule in
	// the service layer).
	List *PriceListInfo
}

// CalculatedPrice is the result of a calculation.
type CalculatedPrice struct {
	// PriceID is the id of the selected price.
	PriceID string
	// PriceSetID is the id of the container the price belongs to.
	PriceSetID string
	// CurrencyCode is the currency of the selected price (UPPERCASE).
	CurrencyCode string
	// Amount is the minor unit amount per unit.
	Amount int64
	// Quantity is the quantity the calculation was made for.
	Quantity int32
	// Total = Amount × Quantity; it is guaranteed not to overflow by
	// [MaxAmount]/[MaxQuantity].
	Total int64
	// MinQuantity is the lower quantity bound of the selected price.
	MinQuantity int32
	// MaxQuantity is the upper quantity bound of the selected price; if nil it is
	// unbounded.
	MaxQuantity *int32
	// PriceListID is the id of the list, if the price comes from a list.
	PriceListID *string
	// PriceListType is the type of the list, if the price comes from a list; it
	// is empty for a base price.
	PriceListType PriceListType
	// MatchedRules is the number of matched rules of the selected price; if 0 the
	// price is unconditional. It explains WHY the selection fell to that price.
	MatchedRules int
}
