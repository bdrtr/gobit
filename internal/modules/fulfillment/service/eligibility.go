package service

import (
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// The BUILT-IN field names of the eligibility context.
//
// The administrator writing a rule uses these names; each one is a FACT of the
// cart and is written OVER the free-form fields the caller sent (see
// [Service.ListShippingOptionsFor]).
const (
	// AttrRegionID is the cart's region.
	AttrRegionID = "region_id"
	// AttrCountryCode is the delivery country (ISO 3166-1 alpha-2, upper case).
	AttrCountryCode = "country_code"
	// AttrCurrencyCode is the cart's currency (ISO 4217, upper case).
	AttrCurrencyCode = "currency_code"
	// AttrSubtotal is the cart's subtotal (minor unit INTEGER). "Free shipping"
	// rules look at this field.
	AttrSubtotal = "subtotal"
	// AttrItemCount is the total number of items in the cart.
	AttrItemCount = "item_count"
	// AttrTotalWeight is the total weight of the shipment (grams).
	AttrTotalWeight = "total_weight"
	// AttrIsReturn is whether we are in a return flow ("true"/"false").
	AttrIsReturn = "is_return"
)

// clientDeclarableFacts are the numeric cart facts the caller can FREELY CLAIM.
//
// These three fields are the HIDDEN state of a cart: this module cannot compute
// them (Principle 2.1) and cannot verify them. Once a rule is bound to them, a
// caller that makes the fact up can open an option that is closed to them. On
// untrusted surfaces ([ListOptionsInput.TrustedFacts] false), options that have
// a rule bound to these fields DO NOT ENTER the list at all.
//
// Region, country and currency are NOT in the list and that is deliberate: they
// are not a privilege gate but the SCOPE of the request — asking for another
// country's options is normal behavior on the storefront, and the delivery
// address is verified at the payment step anyway.
var clientDeclarableFacts = []string{AttrSubtotal, AttrItemCount, AttrTotalWeight}

// ListOptionsInput is a query for the options eligible for a cart context.
//
// All of the fields are facts THE CALLER knows; this module computes none of
// them itself (Principle 2.1: the cart is the cart module's data). The RANGE of
// the numeric fields is nevertheless validated: even though the truth of a value
// cannot be known, it can be known that it is not large enough to overflow the
// provider's arithmetic with a single request parameter.
type ListOptionsInput struct {
	// RegionID is the cart's region. Options whose region equals this AND
	// options whose region is empty become candidates.
	RegionID string
	// CurrencyCode is the cart's currency (ISO 4217); it is required.
	//
	// Having it as a filter is a requirement: adding a shipping option priced in
	// another currency to the cart would mean summing amounts of two currencies.
	CurrencyCode string
	// CountryCode is the delivery country; it is handed to the provider and
	// enters the rule context. It may be empty.
	CountryCode string
	// ShippingProfileIDs are the profiles the cart's products are bound to.
	// If given EMPTY, no profile filter is applied.
	ShippingProfileIDs []string
	// Subtotal is the cart's subtotal (minor unit INTEGER);
	// it has to be between 0 and [models.MaxAmount].
	Subtotal int64
	// ItemCount is the total number of items in the cart;
	// it has to be between 0 and [models.MaxItemCount].
	ItemCount int64
	// TotalWeight is the total weight of the shipment (grams); zero if unknown.
	// It has to be between 0 and [models.MaxTotalWeight].
	TotalWeight int64
	// Attributes is the free-form rule context the caller added
	// (e.g. {"customer_group_id": "vip"}).
	Attributes map[string]string
	// TrustedFacts says that the numeric cart facts
	// ([ListOptionsInput.Subtotal], [ListOptionsInput.ItemCount],
	// [ListOptionsInput.TotalWeight]) were produced on the SERVER side.
	//
	// The default is false and that is deliberate: when a surface forgets to set
	// this flag, the outcome has to fall on the SAFE side. While it is false the
	// numbers are an unverified CLAIM, and options that have a rule bound to
	// them do not enter the list at all (see
	// [Service.ListShippingOptionsFor]).
	//
	// The only parties that pass true: the flows that fetch the cart facts from
	// their own record ([Interop.ListOptionsJSON]) and the admin surface (the
	// administrator can already see the whole catalog, so making the context up
	// opens nothing new to them).
	TrustedFacts bool
	// IncludeAdminOnly is true only on the ADMIN surface.
	IncludeAdminOnly bool
	// IsReturn says whether the return options or the normal options are being
	// asked for.
	IsReturn bool
}

// QuotedOption is a shipping option whose price has been determined.
type QuotedOption struct {
	// Option is the option itself.
	Option models.ShippingOption
	// Amount is the option's fee for this cart (minor unit INTEGER).
	Amount int64
	// CurrencyCode is the currency of the fee (ISO 4217).
	CurrencyCode string
	// ProviderData is the raw data the provider returned; it is filled in only
	// on "calculated" options.
	//
	// IT IS THE PROVIDER'S INTERNAL DATA and DOES NOT REACH the storefront
	// surface (see the api package). The reason it is carried here is so that
	// the same data can be handed back to the provider while the fulfillment is
	// being opened.
	ProviderData json.RawMessage
}

// ListShippingOptionsFor returns the shipping options ELIGIBLE for a cart
// context together with their prices.
//
// # Elimination order
//
//  1. The cheap, column-level eliminations are done IN THE DATABASE: region,
//     currency, return flag, profile set and admin_only. The admin_only filter
//     standing in SQL is deliberate — it is the only field that must not leak to
//     the storefront surface, and never reading the row is safer than reading it
//     and discarding it afterwards.
//  2. ALL the rules of the remaining candidates have to match the context. An
//     option with no rules is unconditional. If the field a rule looks at is
//     absent from the context the rule does not match and the option is
//     eliminated — even on negative operators such as "ne". Otherwise a request
//     with an empty context would satisfy every negative rule and open the
//     restricted options to everyone.
//  3. The fee is determined: "flat" options use their own amount, "calculated"
//     options call the provider's Quote.
//
// # Context fields
//
// The cart's FACTS ([AttrRegionID], [AttrSubtotal], [AttrItemCount], …) are
// written OVER the free-form fields the caller sent. The caller cannot dodge the
// rule by putting its own "subtotal" value inside
// [ListOptionsInput.Attributes]: this method sets the fact.
//
// # In an untrusted context a rule-bound option IS NOT LISTED
//
// The overwrite rule above guarantees not that the fact is TRUE, only that it
// comes from a single place. If the party making the number up is the caller
// itself (a query parameter arriving from the storefront) the rule would be
// dodged all the same: a customer sending "subtotal=50000" with an empty cart
// would see free shipping and its price.
//
// That is why, while [ListOptionsInput.TrustedFacts] is false, an option that
// has a rule bound to the [AttrSubtotal], [AttrItemCount] or [AttrTotalWeight]
// field DOES NOT ENTER the list even if the context matches. The price is plain
// and has been accepted: "free shipping over 500 TRY" never appears on the HTTP
// storefront end; it is shown to the customer through the cart flow
// ([Interop.ListOptionsJSON], with server-side facts). The decision is the same
// as in the pricing module: there too, rule-bound prices never leave the
// storefront surface.
//
// # A provider error DROPS THE OPTION, not the request
//
// If the Quote of a "calculated" option blows up, only THAT OPTION leaves the
// list; the request does not return an error. The rationale: this method is
// called every time the cart is updated, and a single carrier being unreachable
// must not shut down the entire payment step — the "flat" options stay standing
// and the customer can complete the purchase.
//
// The price is plain and has been accepted: a MISCONFIGURED provider looks like
// "there are no options at all". That is why every dropped option is LOGGED; a
// provider not being registered at all is a setup error and is written at ERROR
// level, while a transient Quote failure is written at WARN.
//
// # Ordering
//
// The result is sorted FIRST by fee (the cheaper one wins), and on a tie by
// identifier. The ordering is total: the same input gives the same list on every
// call and the option order on the storefront does not shift from request to
// request.
func (s *Service) ListShippingOptionsFor(
	ctx context.Context,
	in ListOptionsInput,
) ([]QuotedOption, error) {
	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return nil, err
	}
	if err := checkTextLen("the region identifier", in.RegionID); err != nil {
		return nil, err
	}
	if err := requireAmount("the subtotal", in.Subtotal); err != nil {
		return nil, err
	}
	// The UPPER bound of the count and the weight is checked as well. A check
	// that only looks at negativity lets a single query parameter
	// (total_weight=2^63-1) overflow the provider's product: the option would
	// silently drop out of the list and ERROR noise triggered by client input
	// would build up on the server.
	if err := requireRange("the item count", in.ItemCount, models.MaxItemCount); err != nil {
		return nil, err
	}
	if err := requireRange("the total weight", in.TotalWeight, models.MaxTotalWeight); err != nil {
		return nil, err
	}

	countryCode := strings.ToUpper(strings.TrimSpace(in.CountryCode))
	if err := checkTextLen("the country code", countryCode); err != nil {
		return nil, err
	}

	candidates, err := s.store.ListEligibleShippingOptions(ctx, models.EligibilityFilter{
		RegionID:         strings.TrimSpace(in.RegionID),
		CurrencyCode:     currency,
		ProfileIDs:       in.ShippingProfileIDs,
		IsReturn:         in.IsReturn,
		IncludeAdminOnly: in.IncludeAdminOnly,
	})
	if err != nil {
		return nil, err
	}

	attributes := s.eligibilityAttributes(in, currency, countryCode)

	out := make([]QuotedOption, 0, len(candidates))
	for i := range candidates {
		option := candidates[i]
		if !in.TrustedFacts && dependsOnDeclaredFacts(option.Rules) {
			continue
		}
		if !matchRules(option.Rules, attributes) {
			continue
		}
		quoted, ok := s.quote(ctx, option, in, currency, countryCode)
		if !ok {
			continue
		}
		out = append(out, quoted)
	}

	slices.SortFunc(out, func(a, b QuotedOption) int {
		if c := cmp.Compare(a.Amount, b.Amount); c != 0 {
			return c
		}
		return strings.Compare(a.Option.ID, b.Option.ID)
	})
	return out, nil
}

// eligibilityAttributes builds the rule context.
//
// The caller's free-form fields are put in first and the cart's FACTS are
// written afterwards; on a clash the fact wins (see
// [Service.ListShippingOptionsFor]).
func (s *Service) eligibilityAttributes(
	in ListOptionsInput,
	currency, countryCode string,
) map[string]string {
	attributes := make(map[string]string, len(in.Attributes)+7)
	maps.Copy(attributes, in.Attributes)

	attributes[AttrRegionID] = strings.TrimSpace(in.RegionID)
	attributes[AttrCountryCode] = countryCode
	attributes[AttrCurrencyCode] = currency
	attributes[AttrSubtotal] = strconv.FormatInt(in.Subtotal, 10)
	attributes[AttrItemCount] = strconv.FormatInt(in.ItemCount, 10)
	attributes[AttrTotalWeight] = strconv.FormatInt(in.TotalWeight, 10)
	attributes[AttrIsReturn] = strconv.FormatBool(in.IsReturn)
	return attributes
}

// quote determines the fee of a single option.
//
// If the second return value is false the option DROPS OUT of the list; the
// reason has been logged. Returning no error is deliberate (see
// [Service.ListShippingOptionsFor]).
func (s *Service) quote(
	ctx context.Context,
	option models.ShippingOption,
	in ListOptionsInput,
	currency, countryCode string,
) (QuotedOption, bool) {
	if option.PriceType == models.PriceFlat {
		return QuotedOption{
			Option:       option,
			Amount:       option.Amount,
			CurrencyCode: option.CurrencyCode,
		}, true
	}

	provider, err := s.providers.Get(option.ProviderID)
	if err != nil {
		// An unregistered provider is a SETUP error: the registration existed
		// when the option was created, now it does not. It must not pass
		// silently, but the whole list must not fall either because of a single
		// option.
		s.log.ErrorContext(ctx, "the shipping option's provider is not registered, the option dropped out of the list",
			"option", option.ID, "provider", option.ProviderID, "error", err)
		return QuotedOption{}, false
	}

	quote, err := provider.Quote(ctx, coreprovider.QuoteInput{
		OptionID:     option.ID,
		CurrencyCode: currency,
		CountryCode:  countryCode,
		TotalWeight:  in.TotalWeight,
		ItemCount:    in.ItemCount,
		Data:         option.Data,
	})
	if err != nil {
		s.log.WarnContext(ctx, "the shipping provider could not quote a price, the option dropped out of the list",
			"option", option.ID, "provider", option.ProviderID, "error", err)
		return QuotedOption{}, false
	}

	if err := validateQuote(quote, currency); err != nil {
		s.log.ErrorContext(ctx, "the shipping provider returned a price outside the contract, the option dropped out of the list",
			"option", option.ID, "provider", option.ProviderID, "error", err)
		return QuotedOption{}, false
	}

	return QuotedOption{
		Option:       option,
		Amount:       quote.Amount,
		CurrencyCode: strings.ToUpper(strings.TrimSpace(quote.CurrencyCode)),
		ProviderData: quote.Data,
	}, true
}

// validateQuote checks the price the provider returned against the contract.
//
// There are two requirements: the currency has to be the same as the one
// REQUESTED, and the amount has to stay within the allowed range. If the
// currency check were skipped, a shipping fee denominated in dollars would be
// silently added to a lira cart and the difference would only be seen in
// accounting.
func validateQuote(quote coreprovider.ShippingQuote, currency string) error {
	quoted := strings.ToUpper(strings.TrimSpace(quote.CurrencyCode))
	if quoted != currency {
		return errors.Internal(CodeProviderContract,
			"the provider returned a price in the currency %q, %q was requested", quote.CurrencyCode, currency)
	}
	if quote.Amount < models.MinAmount || quote.Amount > models.MaxAmount {
		return errors.Internal(CodeProviderContract,
			"the amount the provider returned has to be between %d and %d: %d",
			models.MinAmount, models.MaxAmount, quote.Amount)
	}
	return nil
}

// dependsOnDeclaredFacts reports whether any of the option's rules looks at a
// cart fact the caller can freely claim.
//
// The decision looks at THE RULE'S FIELD, not at the value in the context: the
// answer to "did it match for this cart" would already come from a fabricated
// number. An option with no rules is unconditional and is unaffected by this
// filter.
func dependsOnDeclaredFacts(rules []models.ShippingOptionRule) bool {
	for i := range rules {
		if slices.Contains(clientDeclarableFacts, rules[i].Attribute) {
			return true
		}
	}
	return false
}

// matchRules reports whether ALL of the option's rules match the context.
// An option with no rules is unconditional and always matches.
func matchRules(rules []models.ShippingOptionRule, attributes map[string]string) bool {
	for i := range rules {
		if !matchRule(rules[i], attributes) {
			return false
		}
	}
	return true
}

// matchRule reports whether a single rule matches the context.
//
// If the field the rule looks at is ABSENT from the context the rule does not
// match — even on negative operators such as "ne" (not equal). Otherwise a
// request with an empty context would satisfy every negative rule and open the
// restricted options to everyone.
//
// A VALUELESS rule does not match either, and DOES NOT PANIC. Service validation
// does not produce such a record, but the eligibility calculation has to be
// resilient to every row it reads from the database: a maintenance script
// running SQL directly, or a partial restore, can leave the values empty. The
// rationale is the same as for an unrecognized operator — a condition that
// cannot be read MUST NOT quietly disable the rule and open the option to
// everyone.
func matchRule(rule models.ShippingOptionRule, attributes map[string]string) bool {
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
		return matchNumeric(rule.Operator, value, rule.Values[0])
	default:
		return false
	}
}

// matchNumeric evaluates the numeric operators over INTEGERS.
//
// If either side cannot be converted to an integer the rule DOES NOT MATCH and
// no error is returned: the context comes from outside and a single malformed
// field must not drop the whole shipping list.
//
// The comparison is NUMERIC, not lexical: had "9" and "50000" been compared as
// strings, 9 would come out larger and the free-shipping threshold would be
// inverted. Converting to an INTEGER, in turn, makes the rule not match instead
// of silently accepting a fractional threshold (e.g. "500.5"); money is a
// minor-unit integer and the rule's threshold has to be one too (plan
// Section 8).
func matchNumeric(operator models.RuleOperator, left, right string) bool {
	lhs, err := strconv.ParseInt(strings.TrimSpace(left), 10, 64)
	if err != nil {
		return false
	}
	rhs, err := strconv.ParseInt(strings.TrimSpace(right), 10, 64)
	if err != nil {
		return false
	}

	switch operator {
	case models.OpGt:
		return lhs > rhs
	case models.OpGte:
		return lhs >= rhs
	case models.OpLt:
		return lhs < rhs
	case models.OpLte:
		return lhs <= rhs
	case models.OpEq, models.OpNe, models.OpIn, models.OpNin:
		return false
	default:
		return false
	}
}
