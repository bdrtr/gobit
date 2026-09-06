// Package models holds the domain models of the fulfillment module.
//
// The types here are independent of the database driver: pgtype and
// sqlc-generated types do NOT LEAK in here. The translation is done in the
// repository layer; the service, the API and the tests see only these types.
//
// Money is an INTEGER minor unit everywhere (cents) and the currency sits in a
// separate field (plan Section 8); floating point is used in no field. Times
// are UTC.
//
// # What it does not know
//
// This module does not know WHICH order a fulfillment belongs to.
// [Fulfillment.Reference] is free text, NOT a foreign key (Principle 2.2), and
// its existence is not validated here; the link is established through Module
// Links. The same holds for [ShippingOption.RegionID] (the region module's
// identifier) and [FulfillmentItem.LineItemID] (the order line's identifier).
package models

import (
	"encoding/json"
	"slices"
	"time"
)

// Amount bounds.
//
// The upper bound is not arbitrary: the shipping fee is added to the order
// total and the total MUST FIT in an int64. The ceiling of 10^12 is
// deliberately the same as the ceiling used by the cart, pricing and payment
// modules; because the modules do not import each other, the value is repeated
// here (the accepted cost of ADR 0001).
const (
	// MinAmount is the smallest permitted shipping amount.
	//
	// It is ZERO and that is deliberate: free shipping is a real business
	// decision (unlike the collection amount in payment, zero produces no dead
	// record here). A negative amount, on the other hand, would mean paying the
	// customer for shipping.
	MinAmount int64 = 0
	// MaxAmount is the largest permitted shipping amount (minor unit).
	MaxAmount int64 = 1_000_000_000_000
)

// Item quantity bounds.
const (
	// MinQuantity is the smallest quantity of a fulfillment item.
	MinQuantity int64 = 1
	// MaxQuantity is the largest quantity of a fulfillment item.
	MaxQuantity int64 = 1_000_000
)

// The numeric bounds of the eligibility context.
//
// The bounds are not arbitrary: the shipping fee is computed by MULTIPLYING
// with these two numbers (see internal/modules/fulfillment/manual). An
// unbounded quantity or weight could overflow the product out of an int64 with
// a single request parameter; an overflowed product means a NEGATIVE shipping
// fee — that is, an order that pays the customer.
//
// The values were chosen together with [MaxAmount]: when the largest unit fee
// is multiplied by either of these two ceilings the result is 10^18 and stays
// within an int64 (~9.22×10^18), which means the provider can say "upper bound
// exceeded" BEFORE the overflow.
const (
	// MaxItemCount is the largest total item quantity that may be declared in
	// an eligibility query.
	MaxItemCount int64 = 1_000_000
	// MaxTotalWeight is the largest total weight that may be declared in an
	// eligibility query (GRAMS); 10^9 grams = 1,000 tons and is more than wide
	// enough for a single fulfillment.
	MaxTotalWeight int64 = 1_000_000_000
)

// ProfileType is the type of a shipping profile.
type ProfileType string

// Shipping profile types.
const (
	// ProfileDefault is the store's default profile; products not bound to any
	// other profile fall in here.
	ProfileDefault ProfileType = "default"
	// ProfileGiftCard is for products that require no physical shipment.
	ProfileGiftCard ProfileType = "gift_card"
	// ProfileCustom is a profile the store defines itself (e.g. "heavy
	// freight").
	ProfileCustom ProfileType = "custom"
)

// String returns the textual form of the type.
func (p ProfileType) String() string { return string(p) }

// Valid reports whether the type is a defined value.
func (p ProfileType) Valid() bool {
	switch p {
	case ProfileDefault, ProfileGiftCard, ProfileCustom:
		return true
	default:
		return false
	}
}

// PriceType says WHERE the fee of a shipping option comes from.
type PriceType string

// Shipping option price types.
const (
	// PriceFlat says the fee sits fixed in the option's own Amount field; the
	// provider is NOT contacted at all.
	PriceFlat PriceType = "flat"
	// PriceCalculated says the provider's Quote determines the fee; the option's
	// Amount field is unused and must be zero.
	PriceCalculated PriceType = "calculated"
)

// String returns the textual form of the type.
func (p PriceType) String() string { return string(p) }

// Valid reports whether the type is a defined value.
func (p PriceType) Valid() bool {
	switch p {
	case PriceFlat, PriceCalculated:
		return true
	default:
		return false
	}
}

// RuleOperator is the comparison operator of a shipping option rule.
//
// The operator set is deliberately the same as the one for price rules in the
// pricing module; an administrator should not have to learn two different
// languages in two places. The package is NOT imported (Principle 2.4), the
// definition is repeated here.
type RuleOperator string

// The supported operators.
//
// eq/ne/in/nin are STRING comparisons; gt/gte/lt/lte convert both sides to
// integers and compare NUMERICALLY (e.g. "subtotal" >= "50000"). A context
// value that cannot be converted to a number makes the rule NOT MATCH rather
// than producing an error: the context comes from outside and a single broken
// field must not bring down the whole shipping list.
//
// The numeric comparison is over INTEGERS; money fields such as the subtotal
// are minor units, so they never pass through floating point (plan Section 8).
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

// String returns the textual form of the operator.
func (o RuleOperator) String() string { return string(o) }

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

// ShippingProfile is the container of shipping options.
//
// Products are bound to profiles through Module Links; a profile does NOT KNOW
// which products are bound to it (Principle 2.1). Which options a cart may see
// is derived from the profiles the cart's products are bound to, and that
// derivation is the caller's job, not fulfillment's.
type ShippingProfile struct {
	// ID is the identifier prefixed with "sprof_".
	ID string
	// Name is the profile's display name; it is UNIQUE among living records.
	Name string
	// Type is the profile's type.
	Type ProfileType
	// Metadata is the caller's free-form extra data.
	Metadata map[string]any
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt is the moment of the soft delete; if nil the profile is alive.
	DeletedAt *time.Time
}

// ShippingOption is a shipping option offered to the customer.
type ShippingOption struct {
	// ID is the identifier prefixed with "sopt_".
	ID string
	// Name is the option's display name (e.g. "Standard shipping").
	Name string
	// ProviderID is the identifier of the carrier that will execute the option.
	ProviderID string
	// ShippingProfileID is the profile the option is bound to (intra-module FK).
	ShippingProfileID string
	// PriceType says where the fee comes from.
	PriceType PriceType
	// Amount is the fee on [PriceFlat] options (minor unit). On
	// [PriceCalculated] options it is ZERO and unused; the fee comes from the
	// provider.
	Amount int64
	// CurrencyCode is the ISO 4217 code and is always stored in UPPERCASE.
	CurrencyCode string
	// RegionID is the region the option is valid in; if EMPTY it is valid in
	// every region. It is the region module's identifier and is NOT A FOREIGN
	// KEY (Principle 2.2).
	RegionID string
	// IsReturn says the option is for a RETURN shipment. Return options are not
	// listed in the normal purchase flow.
	IsReturn bool
	// AdminOnly says the option appears only on the admin surface (e.g. "hand
	// delivery"). It does NOT REACH the storefront surface.
	AdminOnly bool
	// Data is configuration belonging to the provider and is passed to the
	// Quote call as is. It does NOT REACH the storefront surface: it is the
	// provider's internal data.
	Data map[string]any
	// Metadata is the store's free-form extra data.
	Metadata map[string]any
	// Rules are the option's conditions; unless ALL of them match, the option is
	// not offered. They are populated only on the eligibility listing and rule
	// reading paths.
	Rules []ShippingOptionRule
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt is the moment of the soft delete; if nil the option is alive.
	DeletedAt *time.Time
}

// ShippingOptionRule is the rule stating under which condition an option is
// offered.
//
// Example: {Attribute: "subtotal", Operator: OpGte, Values: []string{"50000"}} —
// "if the subtotal exceeds 50,000 minor units this option is offered".
type ShippingOptionRule struct {
	// ID is the identifier prefixed with "sorule_".
	ID string
	// ShippingOptionID is the option the rule is bound to.
	ShippingOptionID string
	// Attribute is the name of the field to look at in the eligibility context.
	Attribute string
	// Operator is the comparison operator.
	Operator RuleOperator
	// Values is the right-hand side of the comparison; it holds at least one
	// element.
	Values []string
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt is the moment of the soft delete; if nil the rule is alive.
	DeletedAt *time.Time
}

// Fulfillment is a shipment that has happened.
type Fulfillment struct {
	// ID is the identifier prefixed with "ful_".
	ID string
	// Reference is the identifier of the caller's own record (the order).
	// It is NOT A FOREIGN KEY (Principle 2.2) and is not validated in this
	// module.
	Reference string
	// ShippingOptionID is the shipping option the fulfillment uses
	// (intra-module FK).
	ShippingOptionID string
	// ProviderID is the identifier of the provider that created the
	// fulfillment.
	ProviderID string
	// ExternalID is the shipment identifier on the provider's side; this is the
	// field that matches the two systems up during reconciliation. It is empty
	// until the provider's response arrives.
	ExternalID string
	// Status is the fulfillment's current status.
	Status FulfillmentStatus
	// TrackingNumber and TrackingURL are the tracking information; empty if the
	// provider does not supply it.
	TrackingNumber string
	TrackingURL    string
	// IdempotencyKey prevents the same fulfillment from being created twice.
	IdempotencyKey string
	// ShippedAt, DeliveredAt and CanceledAt are the moments of the respective
	// transition (UTC); nil if the transition has not happened.
	ShippedAt   *time.Time
	DeliveredAt *time.Time
	CanceledAt  *time.Time
	// Data is the provider's raw data; it is stored as is and not interpreted.
	Data json.RawMessage
	// Metadata is the caller's free-form extra data.
	Metadata map[string]any
	// Items are the items that go into the fulfillment.
	Items []FulfillmentItem
	// CreatedAt and UpdatedAt are UTC.
	//
	// There is no DeletedAt: a fulfillment is the record of a shipment that
	// happened and is never deleted. Its retirement is the 'canceled' status
	// together with CanceledAt, which a database CHECK refuses to let go
	// missing. The field and its column existed until 2026-09-06 and nothing
	// had ever written either (docs/gaps.md D18); the argument is at the head
	// of the module's migration 000003.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// FulfillmentItem is a single item that goes into a fulfillment.
type FulfillmentItem struct {
	// ID is the identifier prefixed with "fulitem_".
	ID string
	// FulfillmentID is the fulfillment the item belongs to (intra-module FK).
	FulfillmentID string
	// LineItemID is the identifier of the order line. It is NOT A FOREIGN KEY
	// (Principle 2.2) and is not validated in this module.
	LineItemID string
	// Quantity is the quantity that goes into the fulfillment; it is always
	// positive.
	Quantity int64
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ManualShipment is the shipment in the manual provider's OWN ledger.
//
// This record is not the module's domain data; it is the state of the imitated
// external system. The fulfillment service never touches it, only the manual
// provider reads and writes it (see internal/modules/fulfillment/manual).
type ManualShipment struct {
	// ID is the PROVIDER identifier prefixed with "manful_"; it sits in the
	// module's fulfillment record as ExternalID.
	ID string
	// IdempotencyKey prevents the same shipment from being created twice; it is
	// UNIQUE in the provider's ledger.
	IdempotencyKey string
	// Reference is the identifier of the caller's own record (the fulfillment
	// identifier).
	Reference string
	// OptionID is the shipping option the shipment was opened under.
	OptionID string
	// Status is the shipment's status on the provider's side.
	Status FulfillmentStatus
	// TrackingNumber and TrackingURL are the tracking information the provider
	// produced.
	TrackingNumber string
	TrackingURL    string
	// Data is the free-form data supplied when the shipment was opened. The
	// keys that steer the manual provider's behavior live here (see the manual
	// package).
	Data json.RawMessage
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ShippingLocation is the SHIPPING policy of a stock location.
//
// The module does NOT KNOW where the warehouse is or what it is called: those
// are the inventory module's data and they stay there. What sits here is only
// the warehouse's shipping quality — which regions it serves and in which order
// it is preferred. [ShippingLocation.LocationID] is the inventory module's
// identifier and is NOT A FOREIGN KEY (Principle 2.2), just like
// [ShippingOption.RegionID].
//
// # A warehouse with NO policy is a valid warehouse too
//
// If there is no record at all for a warehouse the default applies: priority
// ZERO and it serves ALL regions. That is why, in an installation with no
// records at all, selection behaves exactly as it did before policies were
// added.
type ShippingLocation struct {
	// LocationID is the inventory module's location identifier and is the
	// primary key: a warehouse has AT MOST one policy.
	LocationID string
	// Priority is the preference order and the SMALLER ONE WINS. Zero is the
	// default; to lift a warehouse above the defaults, give it a NEGATIVE value.
	Priority int64
	// RegionIDs are the shipping regions the warehouse serves and are ordered by
	// IDENTIFIER: the links form a set, the write order is not preserved.
	//
	// If EMPTY the warehouse serves ALL regions — not "none of them"
	// (see [LocationPolicy]). They are the region module's identifiers and are
	// NOT foreign keys.
	RegionIDs []string
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// LocationPolicy holds the facts about a candidate that affect the decision AT
// THE MOMENT of selection.
//
// It is a separate type from [ShippingLocation] because it carries the same
// data for a different question: the admin surface asks "what is this
// warehouse's setting", while the selection path asks "does this warehouse
// serve THIS region and in which position". The two may carry the same fields
// today, but making them one type would also put every field added to the admin
// surface into what the order path reads.
type LocationPolicy struct {
	// LocationID is the warehouse the policy belongs to.
	LocationID string
	// Priority is the preference order; the smaller one comes first.
	Priority int64
	// RegionIDs are the shipping regions the warehouse is bound to and are
	// ordered by IDENTIFIER.
	//
	// Being EMPTY does NOT mean "it serves no region", it means "it serves ALL
	// regions". The distinction could not be expressed with a single flag: a
	// warehouse with no links and a warehouse that has links but not the
	// requested region would fall into the same bucket, and every order in a
	// policy-less installation would be eliminated.
	//
	// The identifiers are carried as they are rather than as a COUNT or a FLAG:
	// when all candidates are eliminated, the error message writes out which
	// regions the warehouses are actually bound to. The identifier of a region
	// that was deleted and reopened matches nowhere, and this is the only way to
	// diagnose that.
	RegionIDs []string
}

// ServesRegion says whether the warehouse serves the given region.
//
// A warehouse with no links serves ALL regions; the rule is the same as
// [ShippingLocation.RegionIDs] and lives in a single place.
func (p LocationPolicy) ServesRegion(regionID string) bool {
	return len(p.RegionIDs) == 0 || slices.Contains(p.RegionIDs, regionID)
}
