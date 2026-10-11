package service

import (
	"context"
	"slices"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// This file is the queue of reports a provider whose customer pays on its own
// page makes about its sessions (ADR 0438).
//
// The provider reports a payment once, at a moment of its choosing, and the
// shopper who paid may be gone by then. The report is what completes the cart:
// the checkout reads the queue, tries the completion, and writes back what
// happened. This module keeps the queue and makes no decision about a cart.

// CodeReportOutcomeUnknown refuses an outcome the queue does not know.
const CodeReportOutcomeUnknown = "payment_report_outcome_unknown"

// CodeReportedSessionClosed answers a report that names only sessions already
// canceled or declined: the customer paid on a page whose session was closed
// meanwhile, and no cart can be completed with it (ADR 0438). The checkout
// repeats it, and internal/arch holds the two equal.
const CodeReportedSessionClosed = "payment_report_session_closed"

// reportOutcomes are the outcomes a report settles with.
var reportOutcomes = []string{
	models.ReportCompleted, models.ReportRefunded, models.ReportClosed, models.ReportLeft, models.ReportGaveUp,
}

// RecordSessionReport opens the report of every session the provider knows by
// externalID that is still open here — pending or authorized — and returns
// the ids of those whose report is open, oldest first. A session reported
// twice keeps its first report, and one whose report settled is not named
// again: a report the checkout gave up on stays given up.
//
// A report that names sessions and none of them open or captured is refused
// with [CodeReportedSessionClosed] when the provider still holds money for the
// id: money taken on a closed session, which reconciliation reads for the day
// after the cancel. One whose money went back — a report the provider sent
// again after the refund — is taken with nothing to do.
func (s *Service) RecordSessionReport(ctx context.Context, providerID, externalID string) ([]string, error) {
	if err := requireText("provider_id", providerID); err != nil {
		return nil, err
	}
	if err := requireText("external_id", externalID); err != nil {
		return nil, err
	}

	sessions, err := s.store.ListSessionsByExternalID(ctx, providerID, externalID)
	if err != nil {
		return nil, err
	}

	var ids []string
	closedOnly := len(sessions) > 0
	firstDue := time.Now().UTC().Add(models.ReportFirstRetry)
	for i := range sessions {
		switch sessions[i].Status {
		case models.SessionPending, models.SessionAuthorized:
		case models.SessionCanceled, models.SessionFailed:
			continue
		default:
			closedOnly = false

			continue
		}
		closedOnly = false
		if _, err := s.store.RecordSessionReport(ctx, sessions[i].ID, firstDue); err != nil {
			return nil, err
		}
		report, err := s.store.GetSessionReport(ctx, sessions[i].ID)
		if err != nil {
			return nil, err
		}
		if report.Outcome == "" {
			ids = append(ids, sessions[i].ID)
		}
	}
	if closedOnly && s.providerHolds(ctx, providerID, externalID) {
		return nil, errors.Conflict(CodeReportedSessionClosed,
			"%q reported a payment for %s, whose every session here is canceled or declined", providerID, externalID)
	}

	return slices.Clip(ids), nil
}

// providerHolds reports whether the provider says it keeps money for its own
// id; a provider that cannot be asked is taken to keep it, so the report is
// refused loudly rather than dropped.
func (s *Service) providerHolds(ctx context.Context, providerID, externalID string) bool {
	provider, err := s.providers.Get(providerID)
	if err != nil {
		return true
	}
	inspector, ok := provider.(coreprovider.SessionInspector)
	if !ok {
		return true
	}
	inspection, err := inspector.InspectSession(ctx, externalID)
	if err != nil {
		return true
	}

	return inspection.CapturedAmount-inspection.RefundedAmount > 0
}

// DiscoverReportedSessions opens the report of every pending session that a
// provider whose customer pays on its page says it took money for, settled in
// the last [UnreportedWindow] before now and reported nowhere yet, and returns
// the ids of the reports it opened.
//
// It is the sweep's half of the queue: a report a provider made while this
// installation could not take it — a process that stopped between the
// provider's answer and the queue's write — is found here by asking the
// provider, as reconciliation asks it. It is a read of each provider's ledger
// and moves nothing.
func (s *Service) DiscoverReportedSessions(ctx context.Context, now time.Time) ([]string, error) {
	providers := s.customerAuthorizing()
	if len(providers) == 0 {
		return nil, nil
	}
	sessions, err := s.store.ListPendingSessionsForReconciliation(
		ctx, now, now.Add(-UnreportedWindow), providers, MaxReconcileLimit)
	if err != nil {
		return nil, err
	}

	var found []string
	firstDue := now.Add(models.ReportFirstRetry)
	for i := range sessions {
		// The listing reads canceled sessions too, for reconciliation; a
		// report completes a cart, and a canceled session completes none.
		if sessions[i].Status != models.SessionPending {
			continue
		}
		provider, err := s.providers.Get(sessions[i].ProviderID)
		if err != nil {
			continue
		}
		inspector, ok := provider.(coreprovider.SessionInspector)
		if !ok {
			continue
		}
		inspection, err := inspector.InspectSession(ctx, sessions[i].ExternalID)
		if err != nil || inspection.CapturedAmount-inspection.RefundedAmount <= 0 {
			continue
		}
		opened, err := s.store.RecordSessionReport(ctx, sessions[i].ID, firstDue)
		if err != nil {
			return found, err
		}
		if opened {
			found = append(found, sessions[i].ID)
		}
	}

	return found, nil
}

// DueSessionReports returns the open reports whose next attempt is due by the
// given moment, soonest first.
func (s *Service) DueSessionReports(ctx context.Context, dueBy time.Time, limit int) ([]models.SessionReport, error) {
	if limit <= 0 || limit > MaxReconcileLimit {
		return nil, errors.Invalid(CodeInvalidInput,
			"the report page has to be between 1 and %d", MaxReconcileLimit)
	}

	return s.store.ListDueSessionReports(ctx, dueBy, int32(limit))
}

// SessionReport returns one session's report; NotFound when it has none.
func (s *Service) SessionReport(ctx context.Context, sessionID string) (models.SessionReport, error) {
	return s.store.GetSessionReport(ctx, sessionID)
}

// OpenSessionAnswer makes the completion secret of a session the checkout
// opened for a cart, keeps its digest, and returns the only copy (ADR 0438).
//
// The secret is what answers the storefront's completion of that payment once
// the provider's report completed the cart, which the storefront otherwise
// meets as the unknown cart's 404 (ADR 0439): the cart's id and the session's
// id are read off logs and pages, and open nothing.
func (s *Service) OpenSessionAnswer(ctx context.Context, sessionID string) (string, error) {
	if err := requireText("session_id", sessionID); err != nil {
		return "", err
	}
	secret, digest, err := models.NewCompletionSecret()
	if err != nil {
		return "", errors.Wrap(err, errors.KindInternal, CodeInvalidInput,
			"no completion secret could be made for session %s", sessionID)
	}
	if err := s.store.RecordSessionAnswer(ctx, sessionID, digest); err != nil {
		return "", err
	}

	return secret, nil
}

// ClaimSessionAnswer spends a session's completion secret, and reports whether
// this call spent it: false when the secret is not the session's, or was spent
// already (ADR 0438). The checkout spends it before it issues the order's
// token to a completion paid with the session, so the secret opens the order
// once and nothing else does.
func (s *Service) ClaimSessionAnswer(ctx context.Context, sessionID, secret string) (bool, error) {
	if err := requireText("session_id", sessionID); err != nil {
		return false, err
	}

	return s.store.ClaimSessionAnswer(ctx, sessionID, models.CompletionSecretDigest(secret))
}

// RecordSessionReportAttempt counts a failed attempt of an open report and sets
// when the next is due. A settled report is left as it is.
func (s *Service) RecordSessionReportAttempt(ctx context.Context, sessionID, failure string, next time.Time) error {
	_, err := s.store.RecordSessionReportAttempt(ctx, sessionID, failure, next)

	return err
}

// SettleSessionReport closes an open report with one of the known outcomes; a
// report already settled keeps the outcome it has.
func (s *Service) SettleSessionReport(ctx context.Context, sessionID, outcome, note string) error {
	if !slices.Contains(reportOutcomes, outcome) {
		return errors.Invalid(CodeReportOutcomeUnknown, "%q is not an outcome a report settles with", outcome)
	}
	_, err := s.store.SettleSessionReport(ctx, sessionID, outcome, note)

	return err
}
