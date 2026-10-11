package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository/paymentdb"
)

// ListSessionsByExternalID returns the sessions a provider knows by one id of
// its own, oldest first (ADR 0438).
func (r *Repository) ListSessionsByExternalID(
	ctx context.Context, providerID, externalID string,
) ([]models.PaymentSession, error) {
	rows, err := r.queries(ctx).ListSessionsByExternalID(ctx, paymentdb.ListSessionsByExternalIDParams{
		ProviderID: providerID, ExternalID: externalID,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the sessions of %s at %s could not be listed", externalID, providerID)
	}

	out := make([]models.PaymentSession, 0, len(rows))
	for i := range rows {
		out = append(out, toSession(rows[i]))
	}

	return out, nil
}

// RecordSessionReport opens a session's report once, first due at firstDue,
// and reports whether this call opened it (ADR 0438).
func (r *Repository) RecordSessionReport(ctx context.Context, sessionID string, firstDue time.Time) (bool, error) {
	n, err := r.queries(ctx).RecordSessionReport(ctx, paymentdb.RecordSessionReportParams{
		SessionID: sessionID, FirstDue: fromTime(firstDue),
	})
	if err != nil {
		return false, classify(err, codeQueryFailed, "the report of session %s could not be recorded", sessionID)
	}

	return n == 1, nil
}

// ListDueSessionReports returns the open reports due by the given moment,
// soonest first.
func (r *Repository) ListDueSessionReports(
	ctx context.Context, dueBy time.Time, limit int32,
) ([]models.SessionReport, error) {
	rows, err := r.queries(ctx).ListDueSessionReports(ctx, paymentdb.ListDueSessionReportsParams{
		DueBy: fromTime(dueBy), RowLimit: limit,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the due session reports could not be listed")
	}

	out := make([]models.SessionReport, 0, len(rows))
	for i := range rows {
		out = append(out, toSessionReport(rows[i]))
	}

	return out, nil
}

// GetSessionReport returns one report; NotFound when the session has none.
func (r *Repository) GetSessionReport(ctx context.Context, sessionID string) (models.SessionReport, error) {
	row, err := r.queries(ctx).GetSessionReport(ctx, sessionID)
	if err != nil {
		return models.SessionReport{}, classify(err, codeQueryFailed, "the report of session %s could not be read", sessionID)
	}

	return toSessionReport(row), nil
}

// RecordSessionReportAttempt counts a failed attempt of an open report and
// sets the next one; it reports whether an open report was there to count.
func (r *Repository) RecordSessionReportAttempt(
	ctx context.Context, sessionID, failure string, next time.Time,
) (bool, error) {
	n, err := r.queries(ctx).RecordSessionReportAttempt(ctx, paymentdb.RecordSessionReportAttemptParams{
		SessionID: sessionID, NextAttemptAt: fromTime(next), LastFailure: failure,
	})
	if err != nil {
		return false, classify(err, codeQueryFailed, "the attempt on session %s could not be recorded", sessionID)
	}

	return n == 1, nil
}

// SettleSessionReport closes an open report with its outcome; it reports
// whether an open report was there to close.
func (r *Repository) SettleSessionReport(ctx context.Context, sessionID, outcome, note string) (bool, error) {
	n, err := r.queries(ctx).SettleSessionReport(ctx, paymentdb.SettleSessionReportParams{
		SessionID: sessionID, Outcome: outcome, Note: note,
	})
	if err != nil {
		return false, classify(err, codeQueryFailed, "the report of session %s could not be settled", sessionID)
	}

	return n == 1, nil
}

func toSessionReport(row paymentdb.PaymentSessionReport) models.SessionReport {
	return models.SessionReport{
		SessionID:     row.SessionID,
		ReportedAt:    toTime(row.ReportedAt),
		Attempts:      int(row.Attempts),
		NextAttemptAt: toTime(row.NextAttemptAt),
		LastFailure:   row.LastFailure,
		Outcome:       row.Outcome,
		SettledAt:     toTimePtr(row.SettledAt),
	}
}

// RecordSessionAnswer keeps the digest of an opened payment's completion
// secret (ADR 0438).
func (r *Repository) RecordSessionAnswer(ctx context.Context, sessionID string, digest []byte) error {
	if err := r.queries(ctx).RecordSessionAnswer(ctx, paymentdb.RecordSessionAnswerParams{
		SessionID: sessionID, SecretDigest: digest,
	}); err != nil {
		return classify(err, codeQueryFailed, "the completion secret of session %s could not be kept", sessionID)
	}

	return nil
}

// ClaimSessionAnswer spends a session's completion secret when the digest is
// its own and it was not spent, and reports whether this call spent it.
func (r *Repository) ClaimSessionAnswer(ctx context.Context, sessionID string, digest []byte) (bool, error) {
	n, err := r.queries(ctx).ClaimSessionAnswer(ctx, paymentdb.ClaimSessionAnswerParams{
		SessionID: sessionID, SecretDigest: digest,
	})
	if err != nil {
		return false, classify(err, codeQueryFailed, "the answer of session %s could not be claimed", sessionID)
	}

	return n == 1, nil
}
