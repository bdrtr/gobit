package checkout

// This file holds the saga's THIRD step: opening the payment collection,
// binding it to the order, opening the session and having the amount held.
//
// The step's quartet (Name/Restore/Invoke/Compensate) stands here together with
// the private helpers only it calls — freeing the hold of a half-finished
// authorization and linking the order to its collection. What all five steps
// share stays in steps.go.

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// authorizePaymentStep opens the payment collection, opens a session and
// authorizes.
type authorizePaymentStep struct {
	w    *Workflows
	plan *checkoutPlan
}

// authorizeOutput is the authorization step's output written to the execution
// record.
type authorizeOutput struct {
	// CollectionID is the identifier of the payment collection.
	CollectionID string `json:"collection_id"`
	// SessionID is the identifier of the payment session.
	SessionID string `json:"session_id"`
	// Status is the session's status as returned by the provider.
	Status string `json:"status"`
	// Authorized is the amount actually held (minor unit), what the tenders
	// that paid first held included.
	Authorized int64 `json:"authorized"`
	// FirstHolds are the tenders that paid before the provider, in the order
	// they held: the gift card, then the customer's balances (ADR 0209, ADR
	// 0269). SessionID is then the provider's session, and empty when they
	// covered everything.
	FirstHolds []firstHold `json:"first_holds,omitempty"`
	// GiftCardSessionID and GiftCardAuthorized are how a record written before
	// ADR 0269 names the gift card's hold. They are read so an execution
	// recovered across the change still finds its card, and never written.
	GiftCardSessionID  string `json:"gift_card_session_id,omitempty"`
	GiftCardAuthorized int64  `json:"gift_card_authorized,omitempty"`
}

// firstHold is a tender that paid before the provider: its session and what it
// held.
type firstHold struct {
	ProviderID string `json:"provider_id"`
	SessionID  string `json:"session_id"`
	Authorized int64  `json:"authorized"`
}

// holdsOf reads the record's first holds, the gift card of a record written
// before ADR 0269 first among them.
func (o authorizeOutput) holdsOf() []firstHold {
	if o.GiftCardSessionID == "" {
		return o.FirstHolds
	}

	return append([]firstHold{{
		ProviderID: GiftCardProviderID, SessionID: o.GiftCardSessionID, Authorized: o.GiftCardAuthorized,
	}}, o.FirstHolds...)
}

// backwards lists the holds' sessions in the reverse of the order they were
// opened in, which is the order they are released in.
func backwards(holds []firstHold) []string {
	ids := make([]string, 0, len(holds))
	for i := len(holds) - 1; i >= 0; i-- {
		ids = append(ids, holds[i].SessionID)
	}

	return ids
}

// Name returns the step's name.
func (s *authorizePaymentStep) Name() string { return StepAuthorizePayment }

// Restore rebuilds the payment collection and the session FROM THE RECORD.
func (s *authorizePaymentStep) Restore(sc *workflow.StepContext, output json.RawMessage) error {
	var out authorizeOutput
	if err := json.Unmarshal(output, &out); err != nil {
		return errors.Wrap(err, errors.KindInternal, CodeSharedStateInvalid,
			"the output of step %q could not be decoded", StepAuthorizePayment)
	}
	holds := out.holdsOf()
	if out.CollectionID == "" || (out.SessionID == "" && len(holds) == 0) {
		return errors.Internal(CodeSharedStateInvalid,
			"the record of step %q holds no collection or session identifier", StepAuthorizePayment)
	}

	sc.Shared[sharedCollectionID] = out.CollectionID
	if out.SessionID != "" {
		sc.Shared[sharedSessionID] = out.SessionID
	}
	if len(holds) > 0 {
		sc.Shared[sharedFirstHolds] = holds
	}

	return nil
}

// Invoke opens the collection, opens the session and has the amount held.
//
// # THE FULL PAYMENT RULE
//
// If the amount held does not cover the amount that must be collected, the step
// FAILS: authorized < plan.Amount. The rule looks at the NUMBER, not at the
// provider's STATUS string, because on a partial authorization the status is
// still "authorized" and a saga that only looked at the status would approve an
// unpaid order — that was the most serious finding in this project.
//
// # The half-finished step
//
// If the authorization blows up or falls short, the session is canceled HERE;
// otherwise a partially held amount would stay dangling on the customer's card,
// and the engine does not compensate a step that fails on its single attempt. If
// the cancellation blows up as well, the error is wrapped with
// [workflow.ErrUncompensated].
//
// If the session cannot be opened, all that is left behind is an EMPTY
// collection and it is not cleaned up: a collection holds no money, it is only a
// ledger line saying "this much was going to be collected", and the payment
// module's surface has no delete.
//
// # EMPTY identifiers are stopped here
//
// If the collection or the session identifier comes back empty the step fails at
// once. This is the CHEAPEST breaking point of the identifiers on the payment
// path: since no authorization has been made yet there is no held amount on the
// customer's card, and the only price is a reservation that gets rolled back.
// Going on with an empty identifier would lead to the capture step saying "I
// could not find the session", or to compensation quietly falling through to a
// no-op.
func (s *authorizePaymentStep) Invoke(ctx context.Context, sc *workflow.StepContext) (any, error) {
	// The cart's customer travels with the collection, and it is EMPTY on a guest
	// order. What needs it is a tender whose funds belong to a person — store
	// credit — because the provider may not take the owner from the client's own
	// payment data (ADR 0152).
	collectionID, err := s.w.payments.CreateCollection(ctx,
		s.plan.CartID, s.plan.CustomerID, s.plan.CurrencyCode, s.plan.Amount)
	if err != nil {
		return nil, err
	}
	if collectionID == "" {
		return nil, errors.Internal(CodeEmptyIdentifier,
			"the payment module returned an EMPTY collection identifier: %s", s.plan.CartID)
	}
	sc.Shared[sharedCollectionID] = collectionID

	if err := s.linkOrderToCollection(ctx, sc, collectionID); err != nil {
		return nil, err
	}

	out := authorizeOutput{CollectionID: collectionID}
	for _, tender := range s.plan.firstTenders() {
		holds, err := s.authorizeFirst(ctx, sc, collectionID, tender, out.FirstHolds)
		if err != nil {
			return nil, err
		}
		out.FirstHolds = holds
		out.Authorized += holds[len(holds)-1].Authorized
		if out.Authorized == s.plan.Amount {
			s.w.log.InfoContext(ctx, "payment authorized before the provider",
				"cart_id", s.plan.CartID, "collection_id", collectionID, "first_holds", len(holds),
				"authorized", out.Authorized, "amount", s.plan.Amount)

			return out, nil
		}
	}

	// The provider's session is opened for what is left: the payment module
	// takes the collection's amount less what the first tenders hold.
	rest := s.plan.Amount - out.Authorized
	held := backwards(out.FirstHolds)
	sessionID, err := s.w.payments.OpenSessionWithData(ctx,
		collectionID, s.plan.PaymentProviderID, sc.ExecutionID, s.plan.PaymentData)
	if err != nil {
		return nil, s.releaseHold(ctx, err, held...)
	}
	if sessionID == "" {
		return nil, s.releaseHold(ctx, errors.Internal(CodeEmptyIdentifier,
			"the payment module returned an EMPTY session identifier: %s (collection %s)",
			s.plan.CartID, collectionID), held...)
	}
	sc.Shared[sharedSessionID] = sessionID

	status, authorized, err := s.w.payments.Authorize(ctx, sessionID)
	if err != nil {
		return nil, s.releaseHold(ctx, err, append([]string{sessionID}, held...)...)
	}
	if authorized < rest {
		return nil, s.releaseHold(ctx, errors.Conflict(CodePaymentUnderauthorized,
			"the amount held does not cover what must be collected: %d < %d (session %s, status %q)",
			authorized, rest, sessionID, status), append([]string{sessionID}, held...)...)
	}

	s.w.log.InfoContext(ctx, "payment authorized",
		"cart_id", s.plan.CartID, "collection_id", collectionID, "session_id", sessionID,
		"authorized", authorized, "held_first", out.Authorized, "amount", s.plan.Amount)

	out.SessionID, out.Status, out.Authorized = sessionID, status, out.Authorized+authorized

	return out, nil
}

// authorizeFirst opens the session of a tender that pays before the provider,
// for what is still unpaid, and has it hold what it can; it returns the holds
// so far with this one last.
//
// Every such tender is PARTIAL: a gift card always, a customer's balance
// because its session is opened asking (ADR 0209, ADR 0269). It holds its
// balance up to the amount, and an empty one declines. The decline comes back
// as an error and stops the payment here, releasing what the tenders before it
// held, rather than passing the tender over — the customer named it, and
// paying its part with the provider instead would be a different payment from
// the one they chose.
func (s *authorizePaymentStep) authorizeFirst(
	ctx context.Context, sc *workflow.StepContext, collectionID string, tender firstTender, before []firstHold,
) ([]firstHold, error) {
	sessionID, err := s.w.payments.OpenSessionWithData(ctx,
		collectionID, tender.providerID, sc.ExecutionID, tender.data)
	if err != nil {
		return nil, s.releaseHold(ctx, err, backwards(before)...)
	}
	if sessionID == "" {
		return nil, s.releaseHold(ctx, errors.Internal(CodeEmptyIdentifier,
			"the payment module returned an EMPTY %s session identifier: %s (collection %s)",
			tender.providerID, s.plan.CartID, collectionID), backwards(before)...)
	}
	holds := append(slices.Clone(before), firstHold{ProviderID: tender.providerID, SessionID: sessionID})
	sc.Shared[sharedFirstHolds] = holds

	_, held, err := s.w.payments.Authorize(ctx, sessionID)
	if err != nil {
		return nil, s.releaseHold(ctx, err, backwards(holds)...)
	}
	holds[len(holds)-1].Authorized = held

	return holds, nil
}

// releaseHold frees the holds of a half-finished authorization, in the order
// given; an empty id is skipped.
//
// The cancellation is retried with the SAME policy as the engine's compensation
// (see [retryCleanup]): leaving the hold on the customer's card dangling because
// of a transient fault is an outcome that would not have happened had the same
// fault been caught in the compensation chain. Every session is tried even when
// one cannot be canceled.
func (s *authorizePaymentStep) releaseHold(ctx context.Context, cause error, sessionIDs ...string) error {
	cctx, cancel := cleanupContext(ctx)
	defer cancel()

	var failed []error
	for _, sessionID := range sessionIDs {
		if sessionID == "" {
			continue
		}
		if err := retryCleanup(cctx, func() error {
			return s.w.payments.Cancel(cctx, sessionID)
		}); err != nil {
			s.w.log.ErrorContext(ctx, "the half-finished payment session could not be canceled; manual intervention is required",
				"cart_id", s.plan.CartID, "session_id", sessionID, "error", err)
			failed = append(failed, errors.Wrap(err, errors.KindOf(err), CodePaymentUnderauthorized,
				"the hold of session %s could not be freed", sessionID))
		}
	}
	if len(failed) > 0 {
		return errors.Wrap(errors.Join(append([]error{cause, workflow.ErrUncompensated}, failed...)...),
			errors.KindInternal, CodePaymentUnderauthorized,
			"a hold of cart %s could not be freed", s.plan.CartID)
	}

	return cause
}

// Compensate cancels the payment session and frees the hold.
//
// # A NO-OP after the capture
//
// The capture CLOSES the hold (see CapturePayment in the payment module): the
// part that is taken turns into a capture, the part that is not is freed. So if
// the capture happened there is NO hold left to free, and attempting the
// cancellation would only produce errors.Conflict. Reporting that money which
// was taken is not compensated is the capture step's job (see
// [capturePaymentStep.Compensate]); having two steps report the same situation
// would produce nothing but noise.

// linkOrderToCollection binds the order to the collection opened for it.
//
// # Why the binding needs a link at all
//
// The collection carries a Reference and the saga puts the CART id there. It is
// free text the payment module never validates, and that module's own godoc
// says where the association belongs: "Principle 2.2 — the link is established
// through Module Links". Until this call existed nothing established it, so
// there was NO path from an order to the money collected for it — an operator
// asking "what was paid on this order" had to know the cart it came from.
//
// # Why a failure fails the STEP
//
// This runs before the authorization, so nothing has been held on the
// customer's card yet and the only cost of failing is a reservation that gets
// rolled back — the same cheap breaking point the empty-identifier checks use.
// Carrying on without the link would produce exactly the state this call
// exists to end: a paid order with no way back to its payment.
//
// # Why the compensation does not remove it
//
// A rolled-back saga cancels the order and leaves the collection standing —
// "a collection holds no money, it is only a ledger line". The link says which
// order that line belonged to, and deleting it would erase the trace of an
// attempt that really happened.
func (s *authorizePaymentStep) linkOrderToCollection(
	ctx context.Context, sc *workflow.StepContext, collectionID string,
) error {
	orderID, err := sharedText(sc, sharedOrderID)
	if err != nil {
		return err
	}
	if orderID == "" {
		return errors.Internal(CodeEmptyIdentifier,
			"the order identifier is empty while linking the payment collection: %s", collectionID)
	}

	if linkErr := s.w.links.Create(ctx, LinkOrderPayment, orderID, collectionID); linkErr != nil {
		return errors.Wrap(linkErr, errors.KindOf(linkErr), CodeLinkFailed,
			"the order could not be linked to its payment collection: %s -> %s",
			orderID, collectionID)
	}

	return nil
}

func (s *authorizePaymentStep) Compensate(ctx context.Context, sc *workflow.StepContext) error {
	skip, err := s.w.skipAfterCapture(ctx, sc, StepAuthorizePayment, s.plan.CartID)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	// The provider's session first, then the first tenders' from the last to
	// the first: the order they were opened in, undone backwards. Every one is
	// tried; one that cannot be canceled does not leave another's hold
	// standing.
	providerSession, err := sharedText(sc, sharedSessionID)
	if err != nil {
		return err
	}
	holds, err := sharedHolds(sc)
	if err != nil {
		return err
	}
	var failed []error
	for _, sessionID := range append([]string{providerSession}, backwards(holds)...) {
		if sessionID == "" {
			continue
		}
		if cancelErr := s.w.payments.Cancel(ctx, sessionID); cancelErr != nil {
			failed = append(failed, cancelErr)

			continue
		}
		s.w.log.InfoContext(ctx, "compensation: payment session canceled",
			"cart_id", s.plan.CartID, "session_id", sessionID)
	}

	return errors.Join(failed...)
}
