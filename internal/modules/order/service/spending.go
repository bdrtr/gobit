package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// SpendingPolicy is the surface that reports the spending rule to be applied to
// the customer PLACING the order.
//
// # Why the interface is defined HERE
//
// The ADR 0001 pattern: the consumer defines its own narrow interface in its own
// package, the concrete type of the provider satisfies it STRUCTURALLY and it is
// resolved from the container BY NAME. The source of the rule is the b2b module
// but this package CANNOT import it; that is why the signature uses only
// primitive and stdlib types and composite data travels as JSON.
//
// # Why this module applies the rule
//
// The rule combines two pieces of information: the LIMIT (b2b's data) and the
// SPEND (this module's data — the sum of the orders placed). Doing the
// combination on the caller's side (in the complete_cart saga, for instance) was
// possible but it would have been wrong: the check and the writing of the order
// would fall into two separate transactions and two concurrent orders could look
// below the limit and exceed it together. Here the check is done INSIDE THE
// TRANSACTION in which the order is written and under the customer lock (see
// [Service.enforceSpendingLimit]).
//
// The second reason is the ESCAPE: in this module the only way to create an
// order is [Service.CreateOrder] (on the admin surface there is NO endpoint that
// opens an order). When the rule is put there it is applied independently of
// which flow opened the order; had it been put into the saga, a second caller
// added later would silently skip the rule.
//
// # IT IS OPTIONAL
//
// The dependency may be left nil and when it is, the behavior is as if the b2b
// module did not exist at all: no read, no lock, no extra decision. A pure B2C
// installation has no such concept as a spending limit and in that installation
// loading one more query onto every order is not free.
//
// # WHO the rule is applied to
//
// Only to orders whose [CreateOrderInput.CustomerID] is filled in. That field is
// today the storefront's DECLARATION and no layer validates it; under which
// condition the rule is NOT applied is written in the trust boundary section of
// the [Service.spendingRuleFor] godoc and in ADR 0008. This is what the
// embedding application that builds the surface needs to know: wiring this
// interface does not BY ITSELF guarantee that the limit is applied.
type SpendingPolicy interface {
	// SpendingLimitJSON returns the rule to be applied to the customer.
	//
	// The body is in the [spendingRule] schema. If the customer has no rule (not
	// a B2B employee or with an unlimited limit) the call SUCCEEDS and returns
	// "limited": false; it does not return an error. Returning an error means
	// that the rule COULD NOT BE READ and the order is rejected.
	SpendingLimitJSON(ctx context.Context, customerID string) (json.RawMessage, error)
}

// spendingRule is the JSON schema of the spending rule to be applied to a
// customer.
//
// The field names MUST be EXACTLY the same as the schema on the provider's
// side; because this package cannot import the provider the compiler cannot
// check the match and the match can only be proven by an integration test (the
// accepted price of ADR 0001).
//
//	{
//	  "limited":        true,
//	  "spending_limit": 500000,                 // minor unit INTEGER
//	  "currency_code":  "TRY",                  // the currency OF THE LIMIT
//	  "window_start":   "2026-09-01T00:00:00Z"  // when EMPTY there is no window
//	}
type spendingRule struct {
	Limited       bool   `json:"limited"`
	SpendingLimit int64  `json:"spending_limit"`
	CurrencyCode  string `json:"currency_code"`
	WindowStart   string `json:"window_start"`

	// windowStart is the parsed start of the window; it does not come from the
	// JSON.
	windowStart *time.Time
}

// spendingRuleFor reads the rule to be applied to the customer and VALIDATES it.
//
// The read is done OUTSIDE the transaction and this is deliberate: the provider
// is another module and it uses its own connection. Waiting for the query of
// another module while holding an open order transaction would mean locking the
// connection from the pool for the duration of an external call.
//
// The price of this is that the limit CAN CHANGE between the reading of the
// limit and the writing of the order: an administrator who lowers the limit at
// exactly that moment still sees the old limit on this order. That is
// acceptable, because what has to be protected is not the most current value of
// the limit but the consistency of the SUM; the sum is read under the lock.
//
// If the policy is not wired (nil) or there is no customer, the rule is
// "unlimited".
//
// # TRUST BOUNDARY: the rule is applied to the customer the caller DECLARES
//
// The customerID this function receives is not a FACT but a CLAIM and this
// module cannot validate it. The source of the identifier is the "customer_id"
// field in the body of the storefront cart; the only identity of the store
// surface is the publishable API key and that represents a SALES CHANNEL, not a
// customer (see corehttp.Principal — there is NO customer id among its fields).
// That is, no layer produces any proof with which the server could say "this
// customer really made the request".
//
// The consequence in a single sentence: the spending limit is applied to the
// purchases that DECLARE A CUSTOMER. It is not applied to a purchase that does
// not declare one, and these two cases were measured — the same cart, the same
// key, the only difference being the field in the body:
//
//	{"country_code":"TR","customer_id":"cus_…"}  -> 409 order_spending_limit_exceeded
//	{"country_code":"TR"}                        -> 200, the order is opened
//
// The escape can be expressed in three forms and all three pass UNDER this
// line: not sending the field at all (a guest), sending somebody else's
// identifier (the spend falls out of THEIR window — this is also the way to
// burn the allowance of an employee who has a limit) and opening a brand new
// guest record with POST /store/v1/customers and sending that (the new record is
// ruleless because it is not tied to any company).
//
// # WHY the closing was not put here
//
// There is nothing that could be closed: the escape is not "a wrong claim" but
// "making no claim at all". Making the declaration MANDATORY does not help
// either — the third form produces a new identifier by sending one more
// request. Tying the claim to PROOF requires a customer session and that is the
// decision of the framework, not of this module; where the responsibility sits
// is written in ADR 0008.
//
// This is why the branch here is not a defect but the place where the boundary
// IS VISIBLE, and it is pinned down by
// TestTrustBoundaryGuestOrderIsNeverAskedForTheSpendingRule: when a layer that
// authenticates the identity is added, this is the first place that has to
// change.
func (s *Service) spendingRuleFor(ctx context.Context, customerID string) (spendingRule, error) {
	// On a guest order there is no rule to apply: the rule is tied to the
	// employee and the identity of the employee is a customer record. The fact
	// that the identity IS NOT AUTHENTICATED is in the trust boundary section of
	// this godoc.
	if s.spending == nil || customerID == "" {
		return spendingRule{}, nil
	}

	payload, err := s.spending.SpendingLimitJSON(ctx, customerID)
	if err != nil {
		// The class is PRESERVED: a temporary fault (Unavailable) and a broken
		// setup (Invalid) are different branches for the caller. In no case does
		// the order PASS — writing without being able to read the rule would mean
		// silently removing the limit.
		return spendingRule{}, errors.Wrap(err, errors.KindOf(err), CodeSpendingPolicyUnavailable,
			"the spending rule could not be read: %s", customerID)
	}

	var rule spendingRule
	if len(payload) == 0 {
		return spendingRule{}, errors.Internal(CodeSpendingPolicyInvalid,
			"the spending rule arrived empty: %s", customerID)
	}
	if err := json.Unmarshal(payload, &rule); err != nil {
		return spendingRule{}, errors.Wrap(err, errors.KindInternal, CodeSpendingPolicyInvalid,
			"the spending rule could not be parsed: %s", customerID)
	}
	if !rule.Limited {
		return spendingRule{}, nil
	}

	if rule.SpendingLimit < 0 {
		return spendingRule{}, errors.Internal(CodeSpendingPolicyInvalid,
			"the spending limit arrived negative: %s -> %d", customerID, rule.SpendingLimit)
	}
	// The code is made unique before it is STORED: the currency of the order has
	// already been folded to UPPER case (see normalizeCreateOrder) and if the two
	// sides stay in different spellings the comparison takes "TRY" and "try" for
	// separate currencies — that is, the provider sending lower case would
	// silently make the limit impossible to apply.
	currency, err := normalizeCurrency(rule.CurrencyCode)
	if err != nil {
		return spendingRule{}, errors.Wrap(err, errors.KindInternal, CodeSpendingPolicyInvalid,
			"the currency of the spending limit could not be read: %s -> %q", customerID, rule.CurrencyCode)
	}
	rule.CurrencyCode = currency
	if rule.WindowStart != "" {
		start, err := time.Parse(time.RFC3339, rule.WindowStart)
		if err != nil {
			return spendingRule{}, errors.Wrap(err, errors.KindInternal, CodeSpendingPolicyInvalid,
				"the start of the spending window could not be parsed: %s -> %q", customerID, rule.WindowStart)
		}
		utc := start.UTC()
		rule.windowStart = &utc
	}
	return rule, nil
}

// checkCurrency verifies that the currency of the order is MEASURABLE against
// the limit.
//
// # Why we do not convert
//
// The limit of the company is expressed in one currency; if the order is placed
// in another currency the two CANNOT BE ADDED UP. Converting requires an
// exchange rate and there is NO such data as an exchange rate in this
// repository — a made-up rate produces a number whose wrongness never becomes
// visible, and the spending limit is exactly the kind of decision that must not
// rest on a wrong number.
//
// # Why we REJECT instead of SKIPPING
//
// The second option was to leave an order in a different currency outside the
// rule. In that case the rule would turn into a door for whoever wanted to
// escape: an employee whose limit was full could keep shopping from a region
// with another currency. A rule that cannot be applied is not skipped silently;
// the fact that it cannot be applied IS SAID.
//
// The price is clear and it is accepted: in a region whose currency differs from
// the company's, an employee with a spending limit CANNOT place an order. The
// right solution is to define a second record (and limit) in that currency for
// that company; not silently unlimited shopping.
func (r spendingRule) checkCurrency(orderCurrency string) error {
	if !r.Limited || r.CurrencyCode == orderCurrency {
		return nil
	}
	return errors.Conflict(CodeSpendingCurrencyMismatch,
		"the spending limit is defined in %s; an order in %s cannot be measured against this limit",
		r.CurrencyCode, orderCurrency)
}

// enforceSpendingLimit reads the customer's spend and ENFORCES the limit.
//
// # It is only called inside the transaction
//
// The call is the FIRST job of the transaction of [Service.writeOrder]. The
// order is this: the customer lock -> read the sum -> compare -> write the
// order. Because the lock is held until the end of the transaction, a second
// order coming in for the same customer waits and reads the sum together with
// the row THE FIRST ONE wrote. Without the lock both concurrent requests would
// look below the limit and both would be written — that is exactly the race
// between the check and the write.
//
// # The comparison
//
// The rule: if the spend within the window + the amount of this order > the
// limit, the order is REJECTED (one that is equal to the limit passes; the limit
// is the CEILING that may be spent).
//
// A subtraction is used instead of an addition: the spend comes from a SUM in
// the database and is not subject to the bounds of a single order amount, that
// is, "spend + amount" can overflow. "spend > limit - amount" on the other hand
// is a subtraction in which both terms are bounded and it cannot overflow.
func (s *Service) enforceSpendingLimit(ctx context.Context, rule spendingRule, in CreateOrderInput) error {
	if !rule.Limited {
		return nil
	}

	if err := s.store.LockCustomerSpending(ctx, in.CustomerID); err != nil {
		return err
	}
	spent, err := s.store.SumCustomerSpend(ctx, in.CustomerID, in.CurrencyCode, rule.windowStart)
	if err != nil {
		return err
	}
	if spent > rule.SpendingLimit-in.Total {
		return errors.Conflict(CodeSpendingLimitExceeded,
			"spending limit exceeded: spend within period %d, order %d, limit %d (%s)",
			spent, in.Total, rule.SpendingLimit, in.CurrencyCode)
	}

	s.log.InfoContext(ctx, "the spending limit was enforced",
		"customer_id", in.CustomerID, "spent", spent, "amount", in.Total,
		"limit", rule.SpendingLimit, "currency_code", in.CurrencyCode)
	return nil
}
