package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// ReceiveReturn records that the returned goods arrived.
//
// # What it does NOT do
//
// It does not put the stock back and it does not refund anything. Both reach
// across modules — inventory and payment — and this module knows neither
// (Principle 2.1/2.4). What this method establishes is the FACT the flow will
// act on: these lines, in these quantities, are physically here.
//
// # Idempotent, and the second call keeps the FIRST moment
//
// A second receive is a no-op rather than a conflict: an operator clicking
// twice has already achieved what they wanted. It is deliberately not a
// re-write, because received_at is the moment the goods arrived and re-stamping
// it would make the record claim they arrived when somebody clicked.
//
// A canceled return cannot be received: the request was withdrawn, and goods
// arriving against a withdrawn request are a new request.
func (s *Service) ReceiveReturn(
	ctx context.Context, returnID, locationID string,
) (models.Return, error) {
	if err := requireID("location_id", locationID); err != nil {
		return models.Return{}, err
	}

	return s.transitionReturn(ctx, returnID, "receiving",
		models.ReturnStatus.ReceiveAction,
		func(ctx context.Context, id string) (models.Return, error) {
			return s.store.ReceiveReturn(ctx, id, locationID)
		})
}

// CancelReturn withdraws the return request.
//
// A RECEIVED return cannot be withdrawn, and that is the entry in the table
// that carries weight: the goods are physically in the warehouse, the record is
// the only thing that says where they came from, and canceling it would not
// un-receive them.
func (s *Service) CancelReturn(ctx context.Context, returnID string) (models.Return, error) {
	return s.transitionReturn(ctx, returnID, "canceling",
		models.ReturnStatus.CancelAction, s.store.CancelReturn)
}

// WHERE the goods arrived is required, and it is required HERE rather than
// derived, because nothing else in the system knows it.
//
// The order carries no location. The reservation knew one, but reservations are
// CONFIRMED at checkout — which consumes them — and their identifiers are not
// kept against the order. And deriving it from the warehouse that shipped would
// be wrong rather than merely unavailable: a customer may return to a different
// one, and the stock has to land where the goods actually are.

// transitionReturn applies one transition under the record's lock.
//
// The three transitions differ only in their table and their write, so the
// lock, the no-op branch and the conflict message live here once. Had each
// method carried its own copy, the "second call keeps the first moment" rule
// would have to be right in three places.
func (s *Service) transitionReturn(
	ctx context.Context,
	returnID, what string,
	action func(models.ReturnStatus) models.AfterSalesAction,
	write func(context.Context, string) (models.Return, error),
) (models.Return, error) {
	if err := requireID("return_id", returnID); err != nil {
		return models.Return{}, err
	}

	var out models.Return
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.store.LockReturn(ctx, returnID)
		if err != nil {
			return err
		}

		switch action(current.Status) {
		case models.AfterSalesNoop:
			s.log.DebugContext(ctx, "the return record is already in the target state, nothing was done",
				"return_id", returnID, "status", current.Status.String(), "action", what)
			out = current

			return nil
		case models.AfterSalesConflict:
			return errors.Conflict(CodeAfterSalesTransition,
				"%s is not possible on a return in status %q (%s)",
				what, current.Status.String(), returnID)
		case models.AfterSalesProceed:
			// Handled below.
		}

		out, err = write(ctx, returnID)

		return err
	})
	if err != nil {
		return models.Return{}, err
	}

	return out, nil
}

// CancelExchange withdraws the exchange request.
//
// # Why this is the only transition an exchange has
//
// It is the only one the framework can honor. Completing an exchange means two
// movements: goods shipped OUT against an order that already exists, and — when
// [models.Exchange.DifferenceDue] is positive — money collected against that
// same order. The first has no capability anywhere in this repository, which is
// why settling a claim with a replacement is refused rather than stamped
// (internal/workflows/returns/claim.go). The second is forbidden by the
// order-to-payment link's one-to-one cardinality, whose own definition names
// this record as the thing that will reopen it one day
// (internal/modules/payment/service/links.go).
//
// Withdrawing needs neither. Nothing ships, nothing is collected, no other
// module is reached: a request was opened and it is taken back. That is why
// this method exists and its sibling does not.
//
// # Idempotent, and the second call keeps the FIRST moment
//
// The rule [Service.ReceiveReturn] states holds here for the same reason: a
// second withdrawal is a no-op rather than a conflict, and re-stamping would
// make the record claim it was withdrawn at the moment somebody clicked twice.
//
// # It does not check the order
//
// Deliberately, and it is the difference from [Service.CreateExchange], which
// requires a live order. Opening a record against a canceled order would be
// opening work that cannot be done; taking one back is closing work that should
// not be done, and refusing that because the order moved would strand the
// record open forever.
//
// # Why the body is here instead of a third frame
//
// [Service.transitionReturn] and [Service.transitionClaim] exist because their
// record types have TWO transitions each, and the "second call keeps the first
// moment" rule would otherwise have to be right in two places per type. The
// exchange has one. A frame parameterized over a single call site would add an
// indirection and a function value to read past, and would buy no place for the
// rule to go wrong twice.
func (s *Service) CancelExchange(ctx context.Context, exchangeID string) (models.Exchange, error) {
	if err := requireID("exchange_id", exchangeID); err != nil {
		return models.Exchange{}, err
	}

	var out models.Exchange
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.store.LockExchange(ctx, exchangeID)
		if err != nil {
			return err
		}

		switch current.Status.CancelAction() {
		case models.AfterSalesNoop:
			s.log.DebugContext(ctx, "the exchange record is already withdrawn, nothing was done",
				"exchange_id", exchangeID, "status", current.Status.String())
			out = current

			return nil
		case models.AfterSalesConflict:
			return errors.Conflict(CodeAfterSalesTransition,
				"canceling is not possible on an exchange in status %q (%s)",
				current.Status.String(), exchangeID)
		case models.AfterSalesProceed:
			// Handled below.
		}

		out, err = s.store.CancelExchange(ctx, exchangeID)

		return err
	})
	if err != nil {
		return models.Exchange{}, err
	}

	return out, nil
}

// CompleteClaim settles the claim.
//
// It records that the claim WAS settled; what settling meant — money sent back,
// or a replacement shipped — happened outside this module and is the caller's
// to have done. The module cannot check it: both reach modules this one does
// not know.
func (s *Service) CompleteClaim(ctx context.Context, claimID string) (models.Claim, error) {
	return s.transitionClaim(ctx, claimID, "completing",
		models.ClaimStatus.CompleteAction, s.store.CompleteClaim)
}

// CancelClaim withdraws the claim.
func (s *Service) CancelClaim(ctx context.Context, claimID string) (models.Claim, error) {
	return s.transitionClaim(ctx, claimID, "canceling",
		models.ClaimStatus.CancelAction, s.store.CancelClaim)
}

// transitionClaim applies one claim transition under the record's lock.
//
// It is [Service.transitionReturn] for the other record type; the two are
// separate because the statuses are different types, and a shared generic
// would trade a readable table for a type parameter.
func (s *Service) transitionClaim(
	ctx context.Context,
	claimID, what string,
	action func(models.ClaimStatus) models.AfterSalesAction,
	write func(context.Context, string) (models.Claim, error),
) (models.Claim, error) {
	if err := requireID("claim_id", claimID); err != nil {
		return models.Claim{}, err
	}

	var out models.Claim
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.store.LockClaim(ctx, claimID)
		if err != nil {
			return err
		}

		switch action(current.Status) {
		case models.AfterSalesNoop:
			s.log.DebugContext(ctx, "the claim is already in the target state, nothing was done",
				"claim_id", claimID, "status", current.Status.String(), "action", what)
			out = current

			return nil
		case models.AfterSalesConflict:
			return errors.Conflict(CodeAfterSalesTransition,
				"%s is not possible on a claim in status %q (%s)",
				what, current.Status.String(), claimID)
		case models.AfterSalesProceed:
			// Handled below.
		}

		out, err = write(ctx, claimID)

		return err
	})
	if err != nil {
		return models.Claim{}, err
	}

	return out, nil
}

// checkReturnQuantities verifies that the requested lines belong to the order
// and that no line is asked back more times than it was bought.
//
// # Why the rule cannot live in the database
//
// It spans rows: what may be returned depends on every OTHER live return of the
// same line. A CHECK sees only its own row, so the sum is read here — under the
// order's lock, which is what makes reading it and writing against it atomic.
//
// # Why a canceled return does not count
//
// Withdrawing a request releases the units it was holding. A received one does
// not release them, and a still-requested one must count as well: two open
// requests could otherwise each claim a whole line and together ask back twice
// what was bought.
func checkReturnQuantities(
	lines []models.OrderLineItem,
	alreadyReturned map[string]int64,
	requested []ReturnLineInput,
) error {
	ordered := make(map[string]int64, len(lines))
	for i := range lines {
		ordered[lines[i].ID] = lines[i].Quantity
	}

	for i := range requested {
		bought, onOrder := ordered[requested[i].OrderLineItemID]
		if !onOrder {
			return errors.Invalid(CodeReturnLineUnknown,
				"line %s is not on this order", requested[i].OrderLineItemID)
		}

		total := alreadyReturned[requested[i].OrderLineItemID] + requested[i].Quantity
		if total > bought {
			return errors.Conflict(CodeReturnQuantityExceeded,
				"more of line %s was asked back than was bought: %d requested plus %d already, %d bought",
				requested[i].OrderLineItemID, requested[i].Quantity,
				alreadyReturned[requested[i].OrderLineItemID], bought)
		}
	}

	return nil
}

// checkReturnLines validates the shape of the requested lines before any read.
//
// The duplicate check is here rather than left to the unique index: the index
// would reject the SECOND insert, so the transaction would already have written
// the first and the error would name a constraint instead of the mistake.
func checkReturnLines(lines []ReturnLineInput) error {
	seen := make(map[string]bool, len(lines))
	for i := range lines {
		if err := requireID("order_line_item_id", lines[i].OrderLineItemID); err != nil {
			return err
		}
		if lines[i].Quantity <= 0 {
			return errors.Invalid(CodeInvalidInput,
				"the returned quantity has to be positive: line %s, quantity %d",
				lines[i].OrderLineItemID, lines[i].Quantity)
		}
		if err := checkAmount("refund_amount", lines[i].RefundAmount, models.MaxTotal); err != nil {
			return err
		}
		if seen[lines[i].OrderLineItemID] {
			return errors.Invalid(CodeInvalidInput,
				"line %s appears twice in the same return; the quantity carries the count",
				lines[i].OrderLineItemID)
		}
		seen[lines[i].OrderLineItemID] = true
	}

	return nil
}

// lineIDsOf collects the order line ids the request names.
func lineIDsOf(lines []ReturnLineInput) []string {
	out := make([]string, 0, len(lines))
	for i := range lines {
		out = append(out, lines[i].OrderLineItemID)
	}

	return out
}
