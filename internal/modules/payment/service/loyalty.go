package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// The loyalty point ledger (ADR 0164).
//
// The write half is [Service.earnLoyaltyPoints] and it has exactly one caller:
// [Service.writeCollectionTotals], the function every change to a collection's
// captured or refunded total goes through. The module subscribes to no event to
// do this — it publishes and does not subscribe — so the points move inside the
// same transaction as the money they are derived from.
//
// The read half is two operator questions: how many points a customer holds, and
// why. A customer cannot read their own balance, for the reason ADR 0152 already
// recorded for store credit: there is no proven customer identity at the
// storefront, and an endpoint that took the customer's word for who they are
// would be answering about somebody else's account.

// CodeLoyaltyInvalidInput reports a point-ledger read that makes no sense.
//
// It is the only code this ledger has, because it is the only thing a caller can
// get wrong: the write takes no input from anybody — it is derived from a
// collection the module already holds.
const CodeLoyaltyInvalidInput = "payment_loyalty_invalid_input"

// MaxLoyaltyEarnBasisPoints is the highest earn rate an installation may set:
// one point per minor unit of money.
//
// It is a ceiling rather than a clamp, and the difference is the point. A rate
// above it is refused at construction, so an operator who writes one is told;
// silently halving their program — which is what clamping does — would let a
// shop pay out at a rate it never chose and never hear about it.
const MaxLoyaltyEarnBasisPoints int64 = basisPointDivisor

// basisPointDivisor is the denominator of a basis-point rate.
//
// The arithmetic truncates toward zero, as every other basis-point calculation
// in this repository does: a shop never awards a fraction of a point, and the
// remainder is not carried. The direction has to be the SAME in both directions
// — the target is computed once and the ledger stores the difference — or a
// refund would leave a residue row of one point behind forever.
const basisPointDivisor int64 = 10_000

// earnLoyaltyPoints carries one collection's points to the target its money
// implies, appending the DIFFERENCE.
//
// The target is what the collection should have earned by now:
//
//	(captured - refunded) * rate / 10000
//
// and the row it writes is that target minus what the collection has already
// been written. Computing a target rather than an increment is what makes the
// write safe to repeat: a second call with unchanged totals appends nothing, and
// a refund lowers the target so the difference comes out negative. It is the
// same discipline the module's own events already state — carry an identifier,
// read the record — and the reason the inventory ledger dropped its uniqueness
// index (ADR 0162's rule, one module over).
//
// A collection with no customer earns nothing. Most collections have none: a
// guest pays with a card and nobody is named, and a points row keyed on an empty
// customer would put every guest in the shop into one account.
//
// A rate of zero returns before the ledger is read, and that early return is a
// RULE rather than a short circuit. Turning the program off makes the target
// zero, so the difference for a customer who has already earned would come out
// as the negative of everything they hold: the next time their money moved, a
// reverse row would take the lot. Closing a program is not confiscating what it
// gave.
//
// It must only be called INSIDE the collection's own lock, which is where its
// one caller stands.
func (s *Service) earnLoyaltyPoints(ctx context.Context, col models.PaymentCollection) error {
	if s.earnBasisPoints <= 0 || col.CustomerID == "" {
		return nil
	}

	net := col.CapturedAmount - col.RefundedAmount
	if net <= 0 {
		net = 0
	}

	target := net * s.earnBasisPoints / basisPointDivisor
	written, err := s.store.LoyaltyPointsForReference(ctx, col.ID)
	if err != nil {
		return err
	}

	points := target - written
	if points == 0 {
		return nil
	}

	kind := models.LoyaltyEarn
	if points < 0 {
		kind = models.LoyaltyReverse
	}

	entry, err := s.store.AppendLoyaltyEntry(ctx, models.LoyaltyEntry{
		ID:           models.NewLoyaltyEntryID(),
		CustomerID:   col.CustomerID,
		CurrencyCode: col.CurrencyCode,
		Points:       points,
		Kind:         kind,
		Reference:    col.ID,
	})
	if err != nil {
		return err
	}

	s.log.InfoContext(ctx, "loyalty points moved",
		"customer", col.CustomerID, "collection", col.ID,
		"points", points, "kind", kind.String(), "entry", entry.ID)

	return nil
}

// LoyaltyBalance returns a customer's points in ONE currency.
//
// A customer with no rows gets ZERO, and that is not an absence: somebody who
// never earned and somebody whose points were all reversed hold the same number
// of points, and what tells them apart is the ledger itself.
func (s *Service) LoyaltyBalance(
	ctx context.Context, customerID, currencyCode string,
) (int64, error) {
	customerID = strings.TrimSpace(customerID)
	if customerID == "" {
		return 0, errors.Invalid(CodeLoyaltyInvalidInput,
			"the balance needs an owner: customer_id cannot be empty")
	}

	currency, err := normalizeCurrency(currencyCode)
	if err != nil {
		return 0, err
	}

	return s.store.LoyaltyBalance(ctx, customerID, currency)
}

// ListLoyaltyInput is the input for listing a point history.
type ListLoyaltyInput struct {
	// CustomerID and CurrencyCode say which ledger is read.
	CustomerID   string
	CurrencyCode string
	// Page holds the paging parameters.
	Page Page
}

// ListLoyalty returns a customer's point history, newest first.
//
// The history is published alongside the balance for the credit ledger's reason:
// the operator's question is never only "how many" but "why", and that answer is
// the rows — which capture earned them, and which refund took them back.
func (s *Service) ListLoyalty(
	ctx context.Context, in ListLoyaltyInput,
) ([]models.LoyaltyEntry, int64, error) {
	customerID := strings.TrimSpace(in.CustomerID)
	if customerID == "" {
		return nil, 0, errors.Invalid(CodeLoyaltyInvalidInput,
			"the history needs an owner: customer_id cannot be empty")
	}

	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return nil, 0, err
	}

	page, err := in.Page.normalize()
	if err != nil {
		return nil, 0, err
	}

	return s.store.ListLoyaltyEntries(ctx, customerID, currency, page.Limit, page.Offset)
}
