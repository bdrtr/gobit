package models

import (
	"encoding/json"
	"time"
)

// The attributes a segment rule tests about a customer (ADR 0217).
//
// The segment flow evaluates the same words; it cannot import this package, so
// it spells them again, and internal/arch holds the two copies to each other.
const (
	// SegmentHasAccount is whether the customer holds an account (a boolean).
	SegmentHasAccount = "has_account"
	// SegmentAccountAgeDays is how many whole days ago the customer record was
	// created.
	SegmentAccountAgeDays = "account_age_days"
	// SegmentCountryCode is the country of the customer's default shipping
	// address; a customer without one matches no condition on it.
	SegmentCountryCode = "country_code"
	// SegmentOrderCount is how many orders the customer placed in the rule's
	// window, canceled ones left out, in any currency.
	SegmentOrderCount = "order_count"
	// SegmentNetSpend is what those orders in the rule's currency add up to less
	// their refunds, in minor units.
	SegmentNetSpend = "net_spend"
)

// The operators a segment condition compares with.
const (
	SegmentEq  = "eq"
	SegmentNe  = "ne"
	SegmentGt  = "gt"
	SegmentGte = "gte"
	SegmentLt  = "lt"
	SegmentLte = "lte"
	SegmentIn  = "in"
	SegmentNin = "nin"
)

// SegmentOperators is the vocabulary: each attribute and the operators it
// takes.
var SegmentOperators = map[string][]string{
	SegmentHasAccount:     {SegmentEq},
	SegmentAccountAgeDays: {SegmentEq, SegmentNe, SegmentGt, SegmentGte, SegmentLt, SegmentLte},
	SegmentCountryCode:    {SegmentEq, SegmentNe, SegmentIn, SegmentNin},
	SegmentOrderCount:     {SegmentEq, SegmentNe, SegmentGt, SegmentGte, SegmentLt, SegmentLte},
	SegmentNetSpend:       {SegmentEq, SegmentNe, SegmentGt, SegmentGte, SegmentLt, SegmentLte},
}

// The bounds of a segment rule.
const (
	// MaxSegments is how many live groups can be segments at once.
	MaxSegments = 50
	// MaxSegmentConditions is how many conditions one rule holds.
	MaxSegmentConditions = 10
	// MaxSegmentValues is how many countries one in or nin condition lists.
	MaxSegmentValues = 50
	// MaxSegmentWindowDays is the longest window a rule reads orders over.
	MaxSegmentWindowDays = 3650
	// MaxSegmentNumber is the largest number a condition compares with.
	MaxSegmentNumber = 1_000_000_000_000_000
)

// CodeSegmentInvalid reports a segment rule the vocabulary does not allow.
const CodeSegmentInvalid = "customer_segment_invalid"

// CodeSegmentLimit reports a segment that would be one more than MaxSegments.
const CodeSegmentLimit = "customer_segment_limit_reached"

// CodeSegmentManaged reports a hand edit of a segment's members.
const CodeSegmentManaged = "customer_group_segment_managed"

// SegmentRule is the rule a segment's members satisfy: every condition, and
// nothing else.
type SegmentRule struct {
	// CurrencyCode is the currency net_spend is summed in; a rule without a
	// net_spend condition carries none.
	CurrencyCode string `json:"currency_code,omitempty"`
	// WindowDays is how many days back order_count and net_spend read; zero
	// reads the whole history, and a rule without either carries zero.
	WindowDays int32 `json:"window_days,omitempty"`
	// Conditions are ANDed.
	Conditions []SegmentCondition `json:"conditions"`
}

// SegmentCondition compares one attribute of a customer with a value.
type SegmentCondition struct {
	Attribute string `json:"attribute"`
	Operator  string `json:"operator"`
	// Value is a whole number for the numeric attributes, a boolean for
	// has_account and a country code for country_code with eq or ne.
	Value json.RawMessage `json:"value,omitempty"`
	// Values are the country codes of an in or nin condition.
	Values []string `json:"values,omitempty"`
}

// SegmentFacts is what a segment rule reads of a customer's own record.
type SegmentFacts struct {
	CustomerID string
	HasAccount bool
	CreatedAt  time.Time
	// CountryCode is the default shipping address's country, empty without one.
	CountryCode string
}
