package checkout

// This file completes a cart on the server when the provider of its opened
// payment reports that the customer paid (ADR 0438).
//
// A provider whose customer pays on its own page reports the outcome by
// posting back, and the shopper who paid may close the browser before the
// storefront ever calls the completion. Waiting for that call would leave the
// money taken and the order missing until a person repaired it. So the report
// completes the cart itself: the provider's callback hands it here, the
// completion runs at once, and a completion that fails is tried again by the
// sweep with a growing wait, its attempts kept in the payment module's report
// queue. The storefront's own completion of the same session is the same
// completion: whichever comes second answers with the order the first placed.

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// The outcomes a report settles with. They are spelled here for
// [GiftCardProviderID]'s reason, and internal/arch holds them to the payment
// module's.
const (
	ReportCompleted = "completed"
	ReportRefunded  = "refunded"
	ReportClosed    = "closed"
	ReportLeft      = "left"
	ReportGaveUp    = "gave_up"
)

// ReportRetryBase is the wait before the first retry of a completion a report
// could not make; each further retry waits twice the one before.
const ReportRetryBase = 5 * time.Minute

// MaxReportAttempts is how many failed attempts a report's completion is
// given. The waits between them add up to two hours and thirty-five minutes,
// inside the day reconciliation reads a paid session with no order, so a
// report the sweep gave up on is still reported there.
const MaxReportAttempts = 6

// reportPage caps the reports one sweep attempts.
const reportPage = 200

// ReportPass is what one sweep of the report queue did.
type ReportPass struct {
	// Discovered are the paid sessions found by asking their provider, whose
	// report never reached the queue.
	Discovered int
	// Completed, Refunded, Closed and Left are the reports settled this pass,
	// by outcome; Retrying are the ones that failed and wait for another try,
	// and GaveUp the ones whose last try failed.
	Completed, Refunded, Closed, Left, Retrying, GaveUp int
}

// dueReport is the JSON schema of one open report of
// [Payments.DueSessionReportsJSON].
type dueReport struct {
	SessionID string `json:"session_id"`
	Attempts  int    `json:"attempts"`
}

// PaymentReported is the provider's report that the customer paid one of its
// sessions, named by the provider's own id: it opens the report of every open
// session the provider knows by that id and makes its first attempt at
// completing the cart now (ADR 0438).
//
// The attempt runs on a context of its own, detached from the provider's
// request: the provider is told its report was taken whatever the completion
// does, and a completion that fails waits in the queue for the sweep. Only a
// report that could not be recorded at all is an error, so the provider is
// asked to send it again.
func (w *Workflows) PaymentReported(ctx context.Context, providerID, externalID string) error {
	ids, err := w.payments.RecordSessionReport(ctx, providerID, externalID)
	if errors.CodeOf(err) == CodeReportedSessionClosed {
		// The customer paid on a page whose session was canceled meanwhile:
		// no cart can be completed with it, and the money is the provider's
		// to give back. The report was taken; what is left is a person's.
		w.log.ErrorContext(ctx, "a provider reported a payment for a session already closed; "+
			"the money is held at the provider with no order, and reconciliation reports it",
			"provider_id", providerID, "external_id", externalID, "error", err)

		return nil
	}
	if err != nil {
		return err
	}

	attemptCtx, cancel := sagaContext(ctx)
	defer cancel()
	now := time.Now().UTC()
	for _, id := range ids {
		w.attemptReported(attemptCtx, id, now)
	}

	return nil
}

// CompleteReportedPayments is the sweep of the report queue: it first finds
// the paid sessions whose report never reached it, then attempts every open
// report that is due (ADR 0438).
func (w *Workflows) CompleteReportedPayments(ctx context.Context, now time.Time) (ReportPass, error) {
	var pass ReportPass

	// A provider that cannot be asked does not hold back the reports already
	// waiting: they are attempted, and the pass says it failed.
	discovered, discoverErr := w.payments.DiscoverReportedSessions(ctx, now)
	if discoverErr != nil {
		w.log.WarnContext(ctx, "the paid sessions with no report could not be looked for; the due reports are attempted",
			"error", discoverErr)
	}
	pass.Discovered = len(discovered)

	raw, err := w.payments.DueSessionReportsJSON(ctx, now, reportPage)
	if err != nil {
		return pass, err
	}
	var due []dueReport
	if err := json.Unmarshal(raw, &due); err != nil {
		return pass, errors.Wrap(err, errors.KindInternal, CodePaymentAnswerInvalid,
			"the payment module's due reports could not be decoded")
	}

	// A report just found is attempted now, as a report the provider made
	// would have been; the ones already waiting keep their turn.
	attempt := slices.Clone(discovered)
	for _, report := range due {
		if !slices.Contains(attempt, report.SessionID) {
			attempt = append(attempt, report.SessionID)
		}
	}

	for _, sessionID := range attempt {
		switch w.attemptReported(ctx, sessionID, now) {
		case ReportCompleted:
			pass.Completed++
		case ReportRefunded:
			pass.Refunded++
		case ReportClosed:
			pass.Closed++
		case ReportLeft:
			pass.Left++
		case ReportGaveUp:
			pass.GaveUp++
		default:
			pass.Retrying++
		}
	}

	return pass, discoverErr
}

// attemptReported makes one attempt at a report and writes back what came of
// it: the outcome it settled with, or, for a completion that failed, the
// attempt counted with its next try, or the report given up after the last.
// It returns the outcome, empty while the report waits for another try.
func (w *Workflows) attemptReported(ctx context.Context, sessionID string, now time.Time) string {
	outcome, note, err := w.completeReported(ctx, sessionID)
	if err == nil {
		if settleErr := w.payments.SettleSessionReport(ctx, sessionID, outcome, note); settleErr != nil {
			w.log.ErrorContext(ctx, "a reported payment's outcome could not be recorded; the sweep asks again",
				"session_id", sessionID, "outcome", outcome, "error", settleErr)

			return ""
		}
		w.logOutcome(ctx, sessionID, outcome, note)

		return outcome
	}

	attempts, countErr := w.payments.SessionReportAttempts(ctx, sessionID)
	if countErr != nil {
		w.log.ErrorContext(ctx, "a reported payment's attempts could not be read; the sweep asks again",
			"session_id", sessionID, "error", countErr)

		return ""
	}
	attempts++
	if attempts >= MaxReportAttempts {
		if settleErr := w.payments.SettleSessionReport(ctx, sessionID, ReportGaveUp, err.Error()); settleErr != nil {
			w.log.ErrorContext(ctx, "a reported payment could not be given up; the sweep asks again",
				"session_id", sessionID, "error", settleErr)

			return ""
		}
		w.log.ErrorContext(ctx, "a paid payment page's cart could not be completed after the last attempt; "+
			"the money is held with no order, and reconciliation reports it",
			"session_id", sessionID, "attempts", attempts, "error", err)

		return ReportGaveUp
	}

	next := now.Add(ReportRetryBase << (attempts - 1))
	if recordErr := w.payments.RecordSessionReportAttempt(ctx, sessionID, err.Error(), next); recordErr != nil {
		w.log.ErrorContext(ctx, "a reported payment's failed attempt could not be recorded; the sweep asks again",
			"session_id", sessionID, "error", recordErr)
	}
	w.log.WarnContext(ctx, "a paid payment page's cart could not be completed; it is tried again",
		"session_id", sessionID, "attempts", attempts, "next", next, "error", err)

	return ""
}

// logOutcome says what a settled report came to; a refund and a report left
// to a person are what an operator has to know of.
func (w *Workflows) logOutcome(ctx context.Context, sessionID, outcome, note string) {
	switch outcome {
	case ReportRefunded:
		w.log.ErrorContext(ctx, "a reported payment was given back and placed no order",
			"session_id", sessionID, "reason", note)
	case ReportLeft:
		w.log.WarnContext(ctx, "a reported payment was not opened for a cart's checkout and is left to a person",
			"session_id", sessionID, "reason", note)
	case ReportClosed:
		if note == noteHeldOnCanceled {
			w.log.ErrorContext(ctx, "a reported payment's session was canceled while the provider holds the money; "+
				"no order is placed with it",
				"session_id", sessionID)

			return
		}
		w.log.InfoContext(ctx, "a reported payment was settled", "session_id", sessionID, "outcome", outcome)
	default:
		w.log.InfoContext(ctx, "a reported payment was settled", "session_id", sessionID, "outcome", outcome)
	}
}

// completeReported completes the cart a reported session was opened for, and
// returns how the report settles; an error means the attempt failed and is to
// be tried again.
//
// # What it refuses, and gives back
//
// A session the checkout did not open is left to a person: it may be money a
// placed order takes later (ADR 0200), and no cart is behind it. A session
// whose cart is gone, was completed with another payment, or no longer totals
// what was paid can never place an order, so it is canceled — at PayTR that is
// the refund — the stale-session rule the storefront's completion follows,
// applied by the server for a shopper who is not there. Everything else that
// fails — stock taken meanwhile, a fault on the way, another completion of the
// same cart running — is tried again.
func (w *Workflows) completeReported(ctx context.Context, sessionID string) (outcome, note string, err error) {
	facts, err := w.paymentSession(ctx, sessionID)
	if err != nil {
		return "", "", err
	}
	if done, why, settled, err := w.settledBy(ctx, facts); err != nil || settled {
		return done, why, err
	}
	// The opening's key names its own collection; a session opened under
	// any other key, on an order's collection or by an operator, is not a
	// cart's checkout.
	if facts.IdempotencyKey != paymentSessionKeyPrefix+facts.Collection.ID {
		return ReportLeft, "the session was not opened for a cart's checkout", nil
	}

	snap, err := w.snapshot(ctx, facts.Collection.Reference)
	switch {
	case errors.IsNotFound(err):
		return w.refundUnlessTaken(ctx, facts.ID, "the cart the payment was opened for is gone")
	case err != nil:
		return "", "", err
	case snap.Completed:
		return w.refundUnlessTaken(ctx, facts.ID, "the cart was completed with another payment")
	}

	in := CompleteCartInput{
		CartID:            snap.ID,
		PaymentProviderID: facts.ProviderID,
		PaymentSessionID:  facts.ID,
		Email:             snap.Email,
		// The total the payment was opened for is the one the shopper
		// confirmed; a cart that totals anything else refuses the order.
		ExpectedTotal: facts.Amount,
	}
	// The storefront narrows the warehouses by its key's channels; the server
	// narrows by the one the cart was priced in.
	if snap.SalesChannelID != "" {
		in.SalesChannelIDs = []string{snap.SalesChannelID}
	}

	_, err = w.CompleteCart(ctx, in)
	switch code := errors.CodeOf(err); {
	case err == nil:
		return ReportCompleted, "", nil
	case code == CodePaymentSessionStale || code == CodeTotalMismatch || code == CodePaymentSessionForeign ||
		code == CodeCartEmpty:
		return w.refundUnlessTaken(ctx, facts.ID, "the cart no longer totals what was paid: "+err.Error())
	case code == CodeCartCompleted:
		return w.refundUnlessTaken(ctx, facts.ID, "the cart was completed with another payment")
	case code == CodePaymentSessionClosed || code == CodePaymentReportedDeclined:
		return w.settledNow(ctx, facts.ID, err)
	default:
		return "", "", err
	}
}

// noteHeldOnCanceled is the note of a report whose session was canceled while
// the provider still holds its payment.
const noteHeldOnCanceled = "the session was canceled and the provider still holds its payment; " +
	"reconciliation reports it"

// settledBy reports how a session's state settles its report, when it does,
// with the note the report keeps: captured is an order placed, by this path or
// the storefront's; declined is closed by the provider; canceled is asked of
// the provider, because a cancel that met the payment on its way — the
// storefront's, an operator's, a completion's compensation — may have given
// nothing back: refunded when the provider says the payment went back, closed
// with the money held otherwise.
func (w *Workflows) settledBy(
	ctx context.Context, facts paymentSessionFacts,
) (outcome, note string, settled bool, err error) {
	switch facts.Status {
	case SessionCaptured:
		return ReportCompleted, "", true, nil
	case SessionFailed:
		return ReportClosed, "", true, nil
	case SessionCanceled:
		said, err := w.payments.ReportedStatus(ctx, facts.ID)
		if err != nil {
			return "", "", false, err
		}
		if said == SessionCanceled {
			return ReportRefunded, "the session was canceled before a completion took it, which gave the payment back",
				true, nil
		}

		return ReportClosed, noteHeldOnCanceled, true, nil
	}

	return "", "", false, nil
}

// settledNow reads the session again after a refusal that names its state.
func (w *Workflows) settledNow(ctx context.Context, sessionID string, cause error) (outcome, note string, err error) {
	facts, err := w.paymentSession(ctx, sessionID)
	if err != nil {
		return "", "", err
	}
	if done, why, settled, err := w.settledBy(ctx, facts); err != nil || settled {
		return done, why, err
	}

	return ReportClosed, cause.Error(), nil
}

// refundUnlessTaken reads the session again before giving it back, and gives
// it back only when no completion can still take it: a captured session is
// the order's, an authorized one is held by a completion between its
// authorization and its capture, and a completion of the cart running now may
// be about to authorize it. Those are tried again rather than refunded.
func (w *Workflows) refundUnlessTaken(ctx context.Context, sessionID, reason string) (outcome, note string, err error) {
	facts, err := w.paymentSession(ctx, sessionID)
	if err != nil {
		return "", "", err
	}
	if done, why, settled, err := w.settledBy(ctx, facts); err != nil || settled {
		return done, why, err
	}
	if facts.Status == SessionAuthorized {
		return "", "", errors.Conflict(CodePaymentPending,
			"payment session %s is held by a completion that has not captured it", facts.ID)
	}
	running, err := w.completionRunning(ctx, facts.Collection.Reference)
	if err != nil {
		return "", "", err
	}
	if running {
		return "", "", errors.Conflict(CodePaymentPending,
			"a completion of cart %s is running; payment session %s is not given back beside it",
			facts.Collection.Reference, facts.ID)
	}

	return w.refundReported(ctx, facts, reason)
}

// refundReported cancels a reported session that can never place an order,
// which at a provider that took the money is the refund.
func (w *Workflows) refundReported(
	ctx context.Context, facts paymentSessionFacts, reason string,
) (outcome, note string, err error) {
	if cancelErr := w.payments.Cancel(ctx, facts.ID); cancelErr != nil {
		return "", "", cancelErr
	}

	return ReportRefunded, reason, nil
}
