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
// # An open replacement refuses the withdrawal
//
// It is [Service.CancelClaim]'s guard, for the reason that record states: the
// promise outlives the record that made it. ADR 0114 gave the exchange the same
// capability — a replacement may name it as its source and be dispatched
// against it — and this guard did not follow it here until D56. Without it a
// withdrawn exchange still had goods on the way, and nothing between the two
// said so.
func (s *Service) CancelExchange(ctx context.Context, exchangeID string) (models.Exchange, error) {
	return s.transitionExchange(ctx, exchangeID, "canceling",
		models.ExchangeStatus.CancelAction,
		func(ctx context.Context, id string) (models.Exchange, error) {
			open, err := s.openReplacementsOfExchange(ctx, id)
			if err != nil {
				return models.Exchange{}, err
			}
			if len(open) > 0 {
				return models.Exchange{}, errors.Conflict(CodeReplacementNotOpen,
					"exchange %s has an open replacement (%s); withdraw the replacement before "+
						"the exchange, or the promise outlives the record that made it",
					id, open[0].ID)
			}

			return s.store.CancelExchange(ctx, id)
		})
}

// WithdrawFundedExchange takes back an exchange whose difference was funded
// and whose money has been sent back.
//
// # Why this is not CancelExchange
//
// A funded exchange refuses the ordinary withdrawal, and that refusal is the
// point: the customer's money is on the record and taking the request back
// without giving it up would leave money nothing answers for. This verb is the
// exit, and it is FORWARD — the record reaches 'canceled' and keeps both
// moments, so the row still says it once held money and which collection has it.
//
// # Why the money is not checked here
//
// It cannot be: the amount belongs to the payment module and this one may not
// ask it (ADR 0006/0119). The caller is the flow that refunds first and calls
// this after; what this module guarantees is the transition, not the refund.
//
// That is why it takes no route of its own. An operator reaches it through the
// returns flow, which does the refund in the same act.
func (s *Service) WithdrawFundedExchange(ctx context.Context, exchangeID string) (models.Exchange, error) {
	return s.transitionExchange(ctx, exchangeID, "withdrawing a funded exchange",
		func(status models.ExchangeStatus) models.AfterSalesAction {
			switch status {
			case models.ExchangeFunded:
				return models.AfterSalesProceed
			case models.ExchangeCanceled:
				return models.AfterSalesNoop
			default:
				return models.AfterSalesConflict
			}
		},
		s.store.WithdrawFundedExchange)
}

// FundExchange records WHICH payment collection answers the exchange's
// difference, and dates the moment.
//
// # What this call does not do
//
// It does not check that the money is really there, and it cannot: the amount
// belongs to the payment module and this one may not ask it (ADR 0006/0119).
// The caller is the flow that holds both sides; it asks, and this call records
// what it was told. What this module guarantees is everything the ROW can
// answer for — that the difference is positive, that the record was open, and
// that a collection is named exactly once.
//
// # Why a repeat is not simply idempotent
//
// A second call with the SAME collection is the retry path and returns the
// record unchanged. A second call with a DIFFERENT one is refused, and the
// refusal names the collection already recorded.
//
// Treating that as a noop would be the more dangerous silence: a flow that died
// before recording would open a second collection, get "already funded" for an
// answer, and leave money in a collection nothing on this side names — money
// the customer can still be charged through the payment module's own published
// endpoints.
func (s *Service) FundExchange(
	ctx context.Context, exchangeID, collectionID string,
) (models.Exchange, error) {
	if err := requireID("exchange_id", exchangeID); err != nil {
		return models.Exchange{}, err
	}
	if err := requireText("payment_collection_id", collectionID); err != nil {
		return models.Exchange{}, err
	}

	var out models.Exchange
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.store.LockExchange(ctx, exchangeID)
		if err != nil {
			return err
		}

		if current.Status == models.ExchangeFunded {
			if current.PaymentCollectionID != collectionID {
				return errors.Conflict(CodeAfterSalesTransition,
					"exchange %s is already funded by collection %s; a second one cannot be named",
					exchangeID, current.PaymentCollectionID)
			}
			out = current

			return nil
		}

		if current.Status != models.ExchangeRequested {
			return errors.Conflict(CodeAfterSalesTransition,
				"funding is not possible on an exchange in status %q (%s)",
				current.Status.String(), exchangeID)
		}

		if current.DifferenceDue <= 0 {
			return errors.Conflict(CodeAfterSalesTransition,
				"exchange %s owes nothing to collect (difference %d); money owed TO the "+
					"customer leaves by a refund, which is a different act",
				exchangeID, current.DifferenceDue)
		}

		out, err = s.store.FundExchange(ctx, exchangeID, collectionID)

		return err
	})
	if err != nil {
		return models.Exchange{}, err
	}

	return out, nil
}

// CompleteExchange records that the exchange was settled.
//
// # What settling means, and what this module can check
//
// The goods left: a replacement sourced from this exchange was dispatched, which
// is the capability ADR 0090 built and the one migration 000008 named as missing.
// The money either was never owed or has already been collected and recorded
// (ADR 0120) — the database refuses a completion on an exchange whose difference
// is neither (order_exchanges_completed_is_settled), and that refusal is why the
// word is bounded rather than general.
//
// What this module does NOT check is that a replacement really left. The
// dispatch happens in a flow this module cannot see (ADR 0006) and the flow is
// the caller; the same division holds for [Service.CompleteClaim].
//
// # Idempotent, for the reason the withdrawal is
//
// A second settlement keeps the first moment. The flow that calls this recovers
// forward, so a retry after a successful call is the ordinary case rather than a
// mistake.
func (s *Service) CompleteExchange(ctx context.Context, exchangeID string) (models.Exchange, error) {
	return s.transitionExchange(ctx, exchangeID, "completing",
		models.ExchangeStatus.CompleteAction, s.store.CompleteExchange)
}

// transitionExchange is the shared frame of the exchange's two transitions.
//
// It exists for the reason [Service.transitionClaim] does, and it did NOT exist
// while the exchange had one transition: a frame over a single call site is an
// indirection to read past that buys no place for the rule to go wrong twice.
// The completion made it two, and the "second call keeps the first moment" rule
// is now written once instead of in each.
func (s *Service) transitionExchange(
	ctx context.Context,
	exchangeID, what string,
	action func(models.ExchangeStatus) models.AfterSalesAction,
	write func(context.Context, string) (models.Exchange, error),
) (models.Exchange, error) {
	if err := requireID("exchange_id", exchangeID); err != nil {
		return models.Exchange{}, err
	}

	var out models.Exchange
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.store.LockExchange(ctx, exchangeID)
		if err != nil {
			return err
		}

		switch action(current.Status) {
		case models.AfterSalesNoop:
			s.log.DebugContext(ctx, "the exchange record is already in the target state, nothing was done",
				"exchange_id", exchangeID, "status", current.Status.String(), "action", what)
			out = current

			return nil
		case models.AfterSalesConflict:
			return errors.Conflict(CodeAfterSalesTransition,
				"%s is not possible on an exchange in status %q (%s)",
				what, current.Status.String(), exchangeID)
		case models.AfterSalesProceed:
			// Handled below.
		}

		out, err = write(ctx, exchangeID)

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
//
// # A claim with an open replacement is refused
//
// Withdrawing a claim that has promised goods would leave the promise behind:
// the replacement record would stay open, pointing at a claim that says the
// matter is closed. The refusal names the replacement so the operator can
// withdraw it first, which is the same shape the module uses for a return that
// has already been received.
//
// The check is inside the transaction that holds the claim's lock, so a
// replacement created while the withdrawal is deciding is either seen by this
// read or blocked behind it.
func (s *Service) CancelClaim(ctx context.Context, claimID string) (models.Claim, error) {
	return s.transitionClaim(ctx, claimID, "canceling",
		models.ClaimStatus.CancelAction, func(ctx context.Context, id string) (models.Claim, error) {
			open, err := s.openReplacementsOf(ctx, id)
			if err != nil {
				return models.Claim{}, err
			}
			if len(open) > 0 {
				return models.Claim{}, errors.Conflict(CodeReplacementNotOpen,
					"claim %s has an open replacement (%s); withdraw the replacement before "+
						"the claim, or the promise outlives the record that made it",
					id, open[0].ID)
			}

			return s.store.CancelClaim(ctx, id)
		})
}

// openReplacementsOf is the claim's replacements that have not been withdrawn.
func (s *Service) openReplacementsOf(
	ctx context.Context, claimID string,
) ([]models.Replacement, error) {
	all, err := s.store.ListReplacementsByClaim(ctx, claimID)
	if err != nil {
		return nil, err
	}

	return stillOpen(all), nil
}

// openReplacementsOfExchange is the same reading for the other record a
// replacement can be sourced from.
//
// It exists because ADR 0114 gave the exchange what only the claim had — a
// replacement naming it as the source — and the withdrawal guard written for
// the claim did not follow. See [Service.CancelExchange].
func (s *Service) openReplacementsOfExchange(
	ctx context.Context, exchangeID string,
) ([]models.Replacement, error) {
	all, err := s.store.ListReplacementsByExchange(ctx, exchangeID)
	if err != nil {
		return nil, err
	}

	return stillOpen(all), nil
}

// stillOpen keeps the replacements that have not been withdrawn.
func stillOpen(all []models.Replacement) []models.Replacement {
	open := make([]models.Replacement, 0, len(all))
	for i := range all {
		if all[i].Status != models.ReplacementCanceled {
			open = append(open, all[i])
		}
	}

	return open
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
// and that no line is asked back more times than it has units left.
//
// # Why the rule cannot live in the database
//
// It spans rows: what may be returned depends on every OTHER live return of the
// same line AND on every unit written off it. A CHECK sees only its own row, so
// the sum is read here — under the order's lock, which is what makes reading it
// and writing against it atomic.
//
// # Why a canceled unit counts against a return
//
// A unit that will never be delivered cannot come back. Counting only returns
// would let the same three-unit line be asked back twice and canceled once, and
// the goods that arrived at the warehouse would then disagree with the record by
// a quantity nobody could account for. The two acts share one ceiling and
// [Service.unitsSpokenFor] is where they are added.
//
// # Why a canceled return does not count
//
// Withdrawing a request releases the units it was holding. A received one does
// not release them, and a still-requested one must count as well: two open
// requests could otherwise each claim a whole line and together ask back twice
// what was bought.
func checkReturnQuantities(
	lines []models.OrderLineItem,
	spokenFor map[string]int64,
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

		total := spokenFor[requested[i].OrderLineItemID] + requested[i].Quantity
		if total > bought {
			return errors.Conflict(CodeReturnQuantityExceeded,
				"more of line %s was asked back than is left: %d requested plus %d already "+
					"returned or canceled, %d bought",
				requested[i].OrderLineItemID, requested[i].Quantity,
				spokenFor[requested[i].OrderLineItemID], bought)
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
