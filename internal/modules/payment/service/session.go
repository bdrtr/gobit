package service

import (
	"context"
	"encoding/json"
	"strings"

	coreprovider "github.com/bdrtr/gobit/core/provider"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// fieldSessionID is the field name of the session identifier; the SAME name is
// used in validation messages and in error details so that the client does not
// have to learn two different names.
const fieldSessionID = "payment_session_id"

// CreateSessionInput is a request to open a payment session.
type CreateSessionInput struct {
	// Amount is the amount to put on hold (minor unit). If it is given as
	// ZERO, the collection's REMAINING amount is used; it means the same as the
	// "zero = all of it" rule in the provider contract.
	//
	// The remaining amount is found by subtracting the total reserved by OPEN
	// sessions from the collection's amount; this field exists only to SPLIT
	// the payment across more than one session, and their total can never
	// exceed the collection.
	Amount int64
	// IdempotencyKey prevents the same session from being opened twice; it is
	// required.
	IdempotencyKey string
	// Data is provider-specific free-form data (a card token, a return URL,
	// etc.).
	Data map[string]any
}

// CreateSession opens a payment session at a provider for the collection.
//
// A second call with the same (provider, IdempotencyKey) pair DOES NOT OPEN a
// NEW session; the existing session is returned and the provider is not called
// at all (plan Section 2.6). If the key is the same but the collection is
// DIFFERENT, errors.Conflict is returned: idempotency means "repeating the
// same request", not "sending another request with an old key".
//
// # A terminated session's key CANNOT be reused
//
// If the key's session was canceled or declined, errors.Conflict
// ([CodeSessionTerminal]) is returned. Returning the existing session as it is
// would make the caller hit an incomprehensible transition conflict at the
// next step ("authorize"): once the compensation has run, there is no way
// forward with the SAME key. The saga derives a step's key from the execution
// when it retries the step; a flow retried AFTER the compensation has to derive
// a NEW key, and this error code tells it so.
//
// # The remaining amount counts both what was CAPTURED and the OPEN SESSIONS
//
// The amount to open is found by subtracting the captured total AND what live
// sessions have reserved from the collection's amount
// (see [Service.remainingToOpen]); if the remainder is zero no session is
// opened (errors.Conflict), and if no amount is given the session is opened for
// the whole remainder.
//
// Without the reserved share, more than one session, each for the FULL amount,
// could be opened on the same collection, and once all of them were authorized
// there would be a DOUBLE CAPTURE. Without the captured share the same thing
// would happen SEQUENTIALLY: since a capture closes the session's reservation,
// the remainder would look like the full amount again.
//
// Until ADR 0118 the captured share was not here, and in its place stood a
// flag saying "no new session can be opened on a collection whose capture has
// begun". The flag prevented the double capture, but it also made the
// remainder of a PARTIAL capture uncollectable forever; arithmetic tells the
// two cases apart, the flag could not.
//
// Lock order: collection. The provider call is made UNDER this lock (see the
// package documentation for the reasoning).
func (s *Service) CreateSession(
	ctx context.Context,
	collectionID, providerID string,
	in CreateSessionInput,
) (models.PaymentSession, error) {
	if err := requireText("payment_collection_id", collectionID); err != nil {
		return models.PaymentSession{}, err
	}
	if err := requireText("provider_id", providerID); err != nil {
		return models.PaymentSession{}, err
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if err := requireText("idempotency_key", key); err != nil {
		return models.PaymentSession{}, err
	}
	if err := requireOptionalAmount("amount", in.Amount); err != nil {
		return models.PaymentSession{}, err
	}

	prov, err := s.providers.Get(providerID)
	if err != nil {
		return models.PaymentSession{}, err
	}

	var out models.PaymentSession
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		col, err := s.store.LockPaymentCollection(ctx, collectionID)
		if err != nil {
			return err
		}

		existing, err := s.store.PaymentSessionByIdempotencyKey(ctx, prov.ID(), key)
		switch {
		case err == nil:
			if existing.PaymentCollectionID != collectionID {
				return errors.Conflict(CodeIdempotencyMismatch,
					"this idempotency key was used for collection %s: %s",
					existing.PaymentCollectionID, key)
			}
			if existing.Status.Terminal() {
				return errors.Conflict(CodeSessionTerminal,
					"this idempotency key's session is in the %q status; a new key is needed: %s",
					existing.Status, existing.ID).
					WithDetails(map[string]any{
						fieldSessionID: existing.ID,
						"status":       existing.Status.String(),
					})
			}
			s.log.DebugContext(ctx, "the existing payment session was returned",
				"session", existing.ID, "key", key)
			out = existing
			return nil
		case errors.HasKind(err, errors.KindNotFound):
			// The expected branch: the key is being used for the first time.
		default:
			return err
		}

		remaining, reserved, err := s.remainingToOpen(ctx, col)
		if err != nil {
			return err
		}
		if remaining <= 0 {
			return errors.Conflict(CodeCollectionClosed,
				"no amount is left to open on the collection: amount %d, captured %d, reserved by open sessions %d (%s)",
				col.Amount, col.CapturedAmount, reserved, col.ID)
		}
		amount := in.Amount
		if amount == 0 {
			amount = remaining
		}
		if amount > remaining {
			return errors.Conflict(CodeInvalidTransition,
				"the session amount cannot exceed the remaining amount: %d requested, %d remaining (%s)",
				amount, remaining, col.ID)
		}

		session, err := prov.CreateSession(ctx, coreprovider.CreateSessionInput{
			Amount:       amount,
			CurrencyCode: col.CurrencyCode,
			// On the provider side Reference is the COLLECTION identifier; it is
			// the field that matches up the two systems during reconciliation.
			Reference:      col.ID,
			IdempotencyKey: key,
			// The customer comes from the COLLECTION, not from Data (ADR 0152):
			// Data is the client's, and if a tender whose funds belong to a
			// person took its owner from there, a shopper would spend somebody
			// else's balance by writing their name. Most providers never read
			// this field.
			CustomerID: col.CustomerID,
			Data:       in.Data,
		})
		if err != nil {
			return err
		}
		status, err := providerStatus(session.Status, prov.ID())
		if err != nil {
			return err
		}
		if strings.TrimSpace(session.ID) == "" {
			return errors.Internal(CodeProviderContract,
				"provider %q returned a session without an identifier", prov.ID())
		}

		created, err := s.store.CreatePaymentSession(ctx, models.PaymentSession{
			ID:                  models.NewPaymentSessionID(),
			PaymentCollectionID: col.ID,
			ProviderID:          prov.ID(),
			ExternalID:          session.ID,
			Status:              status,
			Amount:              amount,
			CurrencyCode:        col.CurrencyCode,
			Data:                session.Data,
			IdempotencyKey:      key,
		})
		if err != nil {
			return err
		}

		// Derived AFTER the session is written: the count has to see the new
		// session, and the collection has to become "awaiting".
		if _, err := s.writeCollectionTotals(ctx, col,
			col.AuthorizedAmount, col.CapturedAmount, col.RefundedAmount); err != nil {
			return err
		}

		out = created
		return nil
	})
	if err != nil {
		return models.PaymentSession{}, err
	}
	return out, nil
}

// remainingToOpen returns the amount a new session can cover on the
// collection; 0 if none is left. The second return is the total reserved by
// live sessions, and it is given only to write the error message correctly.
//
// The computation subtracts TWO things from the collection's amount: the
// amount ALREADY CAPTURED and what LIVE sessions have reserved.
//
// On the reserved share: looking only at the authorized amount is not enough,
// because while none of them is authorized two sessions, each for the FULL
// amount, could be opened on the same collection; once both were authorized
// twice the collection's amount would be on hold, and once both were captured
// the customer would be charged twice.
//
// # The captured share, and why it was not here until ADR 0118
//
// This computation did NOT read the captured amount AT ALL, and the gate
// against the double capture was held on its own by a line in
// [Service.CreateSession]: "no new session can be opened on a collection whose
// capture has begun". That line read a CUMULATIVE COUNTER, not a BALANCE — not
// "is it holding something right now" but "has it ever taken anything" — and
// it made the remainder of a partially captured collection uncollectable
// forever. Once the arithmetic moved here the line was no longer needed: if the
// remainder is zero the gate closes anyway, and on a fully captured collection
// the remainder is zero.
//
// A REFUND DOES NOT GROW the remainder, because the captured total does not
// change when a refund is written (see [Service.RefundPayment]). This is
// deliberate: making a refunded collection payable again has no consumer today
// (ADR 0063), and the `captured_amount <= amount` constraint is the second wall
// behind the same computation.
//
// It has to be called INSIDE a transaction and UNDER the collection's lock; a
// total read without the lock goes stale when a session opening interleaves.
func (s *Service) remainingToOpen(
	ctx context.Context, col models.PaymentCollection,
) (remaining, reserved int64, err error) {
	reserved, err = s.store.LiveSessionAmount(ctx, col.ID)
	if err != nil {
		return 0, 0, err
	}

	taken := col.CapturedAmount + reserved
	if taken >= col.Amount {
		return 0, reserved, nil
	}

	return col.Amount - taken, reserved, nil
}

// AuthorizePayment puts the session's amount ON HOLD on the customer.
//
// For the transition table see [models.SessionStatus.AuthorizeAction]. For an
// already authorized session the provider IS NOT CALLED and no error is
// returned; an invalid transition (a captured, canceled or declined session)
// returns errors.Conflict.
//
// # A decline IS AN ERROR
//
// If the provider declines, the session is written PERMANENTLY as "failed" and
// the method returns errors.Conflict ([CodeAuthorizationDeclined]). A decline
// is not a server error, but from the caller's point of view the requested
// transition DID NOT HAPPEN; silently returning success would mean that a flow
// which forgets to check the status confirms an unpaid order. The decline
// reason is carried in the error's Details field.
//
// The decline is committed BEFORE the error is returned: the transaction closes
// successfully and the error is produced outside it. Otherwise the rollback
// would erase the decline too, and the session would look "pending" forever.
//
// # Concurrency
//
// Lock order: collection -> session. Of two calls authorizing the same session
// at the same time, EXACTLY ONE goes to the provider; the second sees the
// status the first one wrote and falls into the no-op.
func (s *Service) AuthorizePayment(ctx context.Context, sessionID string) (models.PaymentSession, error) {
	if err := requireText(fieldSessionID, sessionID); err != nil {
		return models.PaymentSession{}, err
	}

	prov, err := s.providerForSession(ctx, sessionID)
	if err != nil {
		return models.PaymentSession{}, err
	}

	var (
		out      models.PaymentSession
		declined *models.PaymentSession
	)
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		col, ses, err := s.lockCollectionAndSession(ctx, sessionID)
		if err != nil {
			return err
		}

		switch ses.Status.AuthorizeAction() {
		case models.ActionNoop:
			s.log.DebugContext(ctx, "the session is already authorized, nothing was done",
				"session", ses.ID)
			out = ses
			return nil
		case models.ActionConflict:
			return conflictTransition("cannot be authorized", ses)
		case models.ActionProceed:
			// Handled below.
		}

		result, err := prov.Authorize(ctx, ses.ExternalID)
		if err != nil {
			return err
		}
		status, err := providerStatus(result.Status, ses.ProviderID)
		if err != nil {
			return err
		}

		switch status {
		case models.SessionAuthorized:
			authorized, err := authorizedAmount(result.AuthorizedAmount, ses)
			if err != nil {
				return err
			}
			updated, err := s.store.UpdatePaymentSessionState(ctx, ses.ID,
				models.SessionAuthorized, authorized, mergeData(ses.Data, result.Data), "")
			if err != nil {
				return err
			}
			if _, err := s.writeCollectionTotals(ctx, col,
				col.AuthorizedAmount+authorized, col.CapturedAmount, col.RefundedAmount); err != nil {
				return err
			}
			out = updated
			return nil

		case models.SessionFailed:
			updated, err := s.store.UpdatePaymentSessionState(ctx, ses.ID,
				models.SessionFailed, 0, mergeData(ses.Data, result.Data), result.DeclineReason)
			if err != nil {
				return err
			}
			if _, err := s.writeCollectionTotals(ctx, col,
				col.AuthorizedAmount, col.CapturedAmount, col.RefundedAmount); err != nil {
				return err
			}
			declined = &updated
			return nil

		default:
			return errors.Internal(CodeProviderContract,
				"provider %q returned the %q status from an authorization; %q or %q was expected",
				ses.ProviderID, status, models.SessionAuthorized, models.SessionFailed)
		}
	})
	if err != nil {
		return models.PaymentSession{}, err
	}
	if declined != nil {
		return models.PaymentSession{}, declineError(*declined)
	}
	return out, nil
}

// CancelPayment closes the session and releases the hold, if there is one.
//
// THIS IS THE SAGA COMPENSATION, and IT IS IDEMPOTENT: for an already canceled
// session no error is returned, the provider is not called a second time and
// the collection's amounts are not touched A SECOND TIME. A compensation step
// has to be re-runnable — when a workflow is retried or triggered twice, the
// second call must not blow up the flow.
//
// For an unknown identifier errors.NotFound is returned: idempotency does not
// mean "silently swallow everything". A REAL session canceled twice and an
// identifier that never existed are different situations, and the second is a
// fault on the caller's side. Since the session record is not deleted (only its
// status changes), the first situation can always be told apart.
//
// A captured session CANNOT be canceled (errors.Conflict): the money has been
// taken, and the way to give it back is [Service.RefundPayment]. A declined
// session, on the other hand, CAN be canceled; it is closed and the decline
// reason is kept in decline_reason.
//
// Lock order: collection -> session.
func (s *Service) CancelPayment(ctx context.Context, sessionID string) error {
	if err := requireText(fieldSessionID, sessionID); err != nil {
		return err
	}

	prov, err := s.providerForSession(ctx, sessionID)
	if err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		col, ses, err := s.lockCollectionAndSession(ctx, sessionID)
		if err != nil {
			return err
		}

		switch ses.Status.CancelAction() {
		case models.ActionNoop:
			s.log.DebugContext(ctx, "the session is already canceled, nothing was done",
				"session", ses.ID)
			return nil
		case models.ActionConflict:
			return conflictTransition("cannot be canceled; use a refund", ses)
		case models.ActionProceed:
			// Handled below.
		}

		if err := prov.Cancel(ctx, ses.ExternalID); err != nil {
			return err
		}

		released := ses.AuthorizedAmount
		if released > col.AuthorizedAmount {
			return errors.Internal(CodeInconsistentState,
				"the collection's held amount (%d) is smaller than the session's (%d) (%s)",
				col.AuthorizedAmount, released, ses.ID)
		}

		if _, err := s.store.UpdatePaymentSessionState(ctx, ses.ID,
			models.SessionCanceled, 0, ses.Data, ses.DeclineReason); err != nil {
			return err
		}
		_, err = s.writeCollectionTotals(ctx, col,
			col.AuthorizedAmount-released, col.CapturedAmount, col.RefundedAmount)
		return err
	})
}

// providerForSession resolves the session's provider.
//
// The session is read OUTSIDE the transaction, without a lock: the only purpose
// here is to learn which provider and which collection are involved. The read
// a decision rests on is always made AGAIN inside the transaction, under the
// lock (see [Service.lockCollectionAndSession]); that is why an interleaving
// change cannot corrupt the decision.
func (s *Service) providerForSession(ctx context.Context, sessionID string) (coreprovider.PaymentProvider, error) {
	ses, err := s.store.GetPaymentSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return s.providers.Get(ses.ProviderID)
}

// lockCollectionAndSession takes the locks in the CANONICAL order: the
// collection first, then the session.
//
// The collection identifier needs an unlocked read of the session; so that the
// order is not inverted, this read is made WITHOUT taking a lock, and the
// session is read again, locked, after the collection is locked.
func (s *Service) lockCollectionAndSession(
	ctx context.Context,
	sessionID string,
) (models.PaymentCollection, models.PaymentSession, error) {
	preview, err := s.store.GetPaymentSession(ctx, sessionID)
	if err != nil {
		return models.PaymentCollection{}, models.PaymentSession{}, err
	}

	col, err := s.store.LockPaymentCollection(ctx, preview.PaymentCollectionID)
	if err != nil {
		return models.PaymentCollection{}, models.PaymentSession{}, err
	}

	ses, err := s.store.LockPaymentSession(ctx, sessionID)
	if err != nil {
		return models.PaymentCollection{}, models.PaymentSession{}, err
	}
	if ses.PaymentCollectionID != col.ID {
		// The session cannot have moved to another collection by the time it is
		// locked; such a deviation is data corruption and must not stay silent.
		return models.PaymentCollection{}, models.PaymentSession{}, errors.Internal(CodeInconsistentState,
			"after collection %s was locked, the session was found in collection %s",
			col.ID, ses.PaymentCollectionID)
	}
	return col, ses, nil
}

// providerStatus converts the core contract's status value into the module's
// status and reports an unrecognized value as a contract violation.
func providerStatus(status coreprovider.SessionStatus, providerID string) (models.SessionStatus, error) {
	converted := models.SessionStatus(status)
	if !converted.Valid() {
		return "", errors.Internal(CodeProviderContract,
			"provider %q returned an unrecognized session status: %q", providerID, status)
	}
	return converted, nil
}

// authorizedAmount validates the held amount the provider reports.
//
// Zero means "all of it"; the same rule the contract sets for Capture and
// Refund is applied here too, so that a provider that leaves the field empty is
// not taken to have held a zero amount. A hold that EXCEEDS the session amount,
// on the other hand, is a contract violation: more would have been held than
// the customer was asked for.
func authorizedAmount(reported int64, ses models.PaymentSession) (int64, error) {
	if reported < 0 {
		return 0, errors.Internal(CodeProviderContract,
			"provider %q returned a negative held amount: %d (%s)",
			ses.ProviderID, reported, ses.ID)
	}
	if reported == 0 {
		return ses.Amount, nil
	}
	if reported > ses.Amount {
		return 0, errors.Internal(CodeProviderContract,
			"provider %q held more than the session amount: %d > %d (%s)",
			ses.ProviderID, reported, ses.Amount, ses.ID)
	}
	return reported, nil
}

// mergeData picks the raw data the provider returned; if it is empty, the
// existing data is kept.
//
// A provider does not have to return a body on every call; erasing the
// existing data with an empty response would lose the information stored when
// the session was opened (e.g. the client_secret the client will use).
func mergeData(current, incoming json.RawMessage) []byte {
	if len(incoming) == 0 {
		return current
	}
	return incoming
}

// conflictTransition builds the shared error for an invalid status transition.
func conflictTransition(action string, ses models.PaymentSession) error {
	return errors.Conflict(CodeInvalidTransition,
		"a payment session in the %q status %s: %s", ses.Status, action, ses.ID).
		WithDetails(map[string]any{
			fieldSessionID: ses.ID,
			"status":       ses.Status.String(),
		})
}

// declineError builds the shared error for a declined authorization.
func declineError(ses models.PaymentSession) error {
	return errors.Conflict(CodeAuthorizationDeclined,
		"the payment was declined: %s (%s)", ses.DeclineReason, ses.ID).
		WithDetails(map[string]any{
			fieldSessionID:   ses.ID,
			"provider_id":    ses.ProviderID,
			"decline_reason": ses.DeclineReason,
		})
}
