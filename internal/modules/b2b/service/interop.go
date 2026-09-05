package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// This file is the CROSS-MODULE surface of the b2b module (ADR 0001).
//
// The side that ENFORCES the spending limit is the order module: the spending
// itself (the sum of the orders placed) is its data and that is the only place
// where the rule can be applied in the same transaction as the write. But WHAT
// the limit is, is this module's data. The two cannot import each other, which
// is why the bond is built the same way as in the interop.go files of the other
// modules: a surface using only PRIMITIVE and stdlib types is published, the
// consumer defines its own narrow interface in its own package and the concrete
// type is resolved from the container under the name "b2b.interop".
//
// The counterpart on the consumer side is this (order defines it in its own
// package):
//
//	type SpendingPolicy interface {
//	    SpendingLimitJSON(ctx context.Context, customerID string) (json.RawMessage, error)
//	}
//
// The composite data travels as JSON. The field names are declared EXPLICITLY
// below; they MUST be exactly the same as the schema on the consumer side and
// the agreement can only be proven with an integration test — because this
// module cannot import the order package, the compiler cannot check it.

// Interop turns the b2b service into the cross-module PRIMITIVE surface.
//
// It takes no decision: it only translates the signature and the JSON schema.
// THIS side does not answer the question "was the limit exceeded"; to be able
// to answer it, it would have to know the order total inside the window and
// that data belongs to the order module.
//
// It is registered in the container under the name "b2b.interop".
type Interop struct {
	svc *Service
}

// NewInterop sets up the cross-module surface for the given service.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// interopSpendingRule is the JSON schema of the spending rule to be applied to
// a customer.
//
// # Schema
//
//	{
//	  "limited":        true,                   // if false the other fields are MEANINGLESS
//	  "spending_limit": 500000,                 // minor unit INTEGER
//	  "currency_code":  "TRY",                  // the COMPANY's currency
//	  "window_start":   "2026-09-01T00:00:00Z"  // EMPTY means there is no window
//	}
//
// # Why "limit + window" and not "remaining allowance"
//
// Computing the remainder requires the sum of the orders placed inside the
// window; that data belongs to the order module. Returning the remainder from
// here would mean b2b reading order (that is, exactly the dependency the link
// layer removed). Instead the surface carries the RULE, and the side that
// applies the rule to the fact is the module that owns the fact.
//
// # What window_start says
//
// The start of the window derives from the COMPANY's reset period and follows
// the CALENDAR (see models.SpendingResetPeriod): a monthly limit resets on the
// 1st of every month, a yearly limit on 1 January, and the window is UTC. If
// the field is an EMPTY string there is no window ([models.ResetNever]) and the
// limit applies to the employee's ENTIRE history — that is the reason an empty
// string was chosen over sending a zero timestamp: "since 1 January 0001" and
// "there is no window" are different sentences, and the second one is not a
// date.
//
// The time is an RFC 3339 string, not an integer: the consumer parses it in a
// single line and does not stay bound to an encoding decision (seconds or
// milliseconds).
//
// # KNOWN LIMIT: the employee who changes company mid-period
//
// The window comes from the calendar, not from the moment the employee STARTED
// WORK. If a customer leaves company A mid-period and is added to company B as
// an employee, the spending they did at A counts inside B's window too —
// because the consumer sums the spending by CUSTOMER identifier and that
// identifier has not changed.
//
// The deviation is one-directional and RESTRICTIVE: the employee may spend LESS
// than they are entitled to, never more. That is why it was deliberately not
// fixed — fixing it means bounding the window by the birth moment of the
// employee record, and that is a separate decision which shifts the definition
// of "period" from the calendar to the relationship (it has to line up with the
// accounting period). The price paid today is an extra restriction seen on a
// few records a year; it is not a decision taken silently.
type interopSpendingRule struct {
	Limited       bool   `json:"limited"`
	SpendingLimit int64  `json:"spending_limit"`
	CurrencyCode  string `json:"currency_code"`
	WindowStart   string `json:"window_start"`
}

// SpendingLimitJSON returns the spending rule to be applied to the customer.
//
// The schema is defined in the [interopSpendingRule] document.
//
// # A customer WITHOUT a rule IS NOT AN ERROR
//
// In all three of these cases the call returns "limited": false and SUCCEEDS:
//
//   - The customer is not an employee of any company (a B2C purchase; this is
//     the majority of an installation).
//   - The customer is an employee but their spending limit is nil, that is,
//     UNLIMITED.
//   - The given identifier is not even a customer id (the prefix does not
//     match). Such an identifier CANNOT BE BOUND as an employee (see
//     [Service.CreateEmployee]), so the answer "this customer has no limit" is
//     not a guess but a provable fact.
//
// Returning an error in all three would be wrong: the consumer calls this
// surface for EVERY order and "this customer is not B2B" is the normal path for
// it. Returning an error would leave the consumer unable to tell "there is no
// rule" from "we could not learn the rule" — the first has to let the order
// through, the second has to STOP it.
//
// Every other error (a database failure, the bond layer not being readable) is
// returned AS IS and the consumer rejects the order. Letting an order through
// at a moment when the rule could not be read would mean silently removing the
// limit.
func (i *Interop) SpendingLimitJSON(ctx context.Context, customerID string) (json.RawMessage, error) {
	membership, err := i.svc.MembershipOfCustomer(ctx, customerID)
	switch {
	case err == nil:
		// The rule is built below.
	case errors.IsNotFound(err), errors.IsInvalid(err):
		return json.Marshal(interopSpendingRule{})
	default:
		return nil, err
	}

	if !membership.Employee.HasSpendingLimit() {
		return json.Marshal(interopSpendingRule{})
	}

	rule := interopSpendingRule{
		Limited:       true,
		SpendingLimit: *membership.Employee.SpendingLimit,
		CurrencyCode:  membership.Company.CurrencyCode,
	}
	if membership.SpendingWindowStart != nil {
		rule.WindowStart = membership.SpendingWindowStart.UTC().Format(time.RFC3339)
	}
	return json.Marshal(rule)
}
