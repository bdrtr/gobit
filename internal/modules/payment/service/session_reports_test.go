package service_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// withExternalID gives a seeded session the provider's own id.
func withExternalID(store *fakeStore, sessionID, externalID string, status models.SessionStatus) {
	store.mu.Lock()
	defer store.mu.Unlock()
	ses := store.sessions[sessionID]
	ses.ExternalID, ses.Status = externalID, status
	store.sessions[sessionID] = ses
}

// TestAReportOpensTheOpenSessionsItNames: a provider's id names every session
// opened under it; the open ones are reported, once each (ADR 0438).
func TestAReportOpensTheOpenSessionsItNames(t *testing.T) {
	svc, store := reconcileFixture(t, newPayingPageProvider("paytr"))
	seedPending(t, store, "ses_pending", "paytr", time.Hour)
	seedPending(t, store, "ses_authorized", "paytr", time.Hour)
	seedPending(t, store, "ses_canceled", "paytr", time.Hour)
	withExternalID(store, "ses_pending", "OID1", models.SessionPending)
	withExternalID(store, "ses_authorized", "OID1", models.SessionAuthorized)
	withExternalID(store, "ses_canceled", "OID1", models.SessionCanceled)

	ids, err := svc.RecordSessionReport(t.Context(), "paytr", "OID1")

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"ses_pending", "ses_authorized"}, ids)
	_, err = svc.SessionReport(t.Context(), "ses_canceled")
	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "a closed session is not reported: %v", err)

	again, err := svc.RecordSessionReport(t.Context(), "paytr", "OID1")
	require.NoError(t, err)
	assert.ElementsMatch(t, ids, again, "a second report names the same sessions and opens nothing new")
	assert.Len(t, store.reports, 2)
}

// TestDiscoveryFindsAPaymentWhoseReportNeverArrived: a pending session its
// provider says it kept money for is reported once; one paid nothing, given
// back whole, or settled before the last day, is not.
func TestDiscoveryFindsAPaymentWhoseReportNeverArrived(t *testing.T) {
	page := newPayingPageProvider("paytr")
	svc, store := reconcileFixture(t, page)
	seedPending(t, store, "ses_paid", "paytr", time.Hour)
	seedPending(t, store, "ses_older_than_a_day", "paytr", service.UnreportedWindow+time.Hour)
	now := time.Now().UTC()

	found, err := svc.DiscoverReportedSessions(t.Context(), now)
	require.NoError(t, err)
	assert.Empty(t, found, "a page nobody paid on is not reported")

	page.inspection = coreprovider.SessionInspection{
		Status: coreprovider.SessionCanceled, CapturedAmount: amount, RefundedAmount: amount,
	}
	found, err = svc.DiscoverReportedSessions(t.Context(), now)
	require.NoError(t, err)
	assert.Empty(t, found, "a payment given back whole is not money held")

	page.inspection = coreprovider.SessionInspection{Status: coreprovider.SessionCaptured, CapturedAmount: amount}
	found, err = svc.DiscoverReportedSessions(t.Context(), now)
	require.NoError(t, err)
	assert.Equal(t, []string{"ses_paid"}, found)

	found, err = svc.DiscoverReportedSessions(t.Context(), now)
	require.NoError(t, err)
	assert.Empty(t, found, "a payment already reported is not found again")
}

// TestAReportSettlesOnceWithAKnownOutcome: an outcome the queue does not know
// is refused, and a settled report keeps the outcome it has.
func TestAReportSettlesOnceWithAKnownOutcome(t *testing.T) {
	svc, store := reconcileFixture(t, newPayingPageProvider("paytr"))
	seedPending(t, store, "ses_1", "paytr", time.Hour)
	withExternalID(store, "ses_1", "OID1", models.SessionPending)
	_, err := svc.RecordSessionReport(t.Context(), "paytr", "OID1")
	require.NoError(t, err)

	err = svc.SettleSessionReport(t.Context(), "ses_1", "maybe", "")
	require.Error(t, err)
	assert.Equal(t, service.CodeReportOutcomeUnknown, coreerrors.CodeOf(err))

	require.NoError(t, svc.SettleSessionReport(t.Context(), "ses_1", models.ReportRefunded, "cart changed"))
	require.NoError(t, svc.SettleSessionReport(t.Context(), "ses_1", models.ReportCompleted, ""))

	report, err := svc.SessionReport(t.Context(), "ses_1")
	require.NoError(t, err)
	assert.Equal(t, models.ReportRefunded, report.Outcome)
	assert.Equal(t, "cart changed", report.LastFailure)
	require.NotNil(t, report.SettledAt)
}

// TestOnlyDueReportsAreAttempted: a report waits until its next try.
func TestOnlyDueReportsAreAttempted(t *testing.T) {
	svc, store := reconcileFixture(t, newPayingPageProvider("paytr"))
	seedPending(t, store, "ses_1", "paytr", time.Hour)
	withExternalID(store, "ses_1", "OID1", models.SessionPending)
	_, err := svc.RecordSessionReport(t.Context(), "paytr", "OID1")
	require.NoError(t, err)
	next := time.Now().UTC().Add(time.Hour)
	require.NoError(t, svc.RecordSessionReportAttempt(t.Context(), "ses_1", "no stock", next))

	due, err := svc.DueSessionReports(t.Context(), next.Add(-time.Second), 10)
	require.NoError(t, err)
	assert.Empty(t, due)

	due, err = svc.DueSessionReports(t.Context(), next, 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, 1, due[0].Attempts)
	assert.Equal(t, "no stock", due[0].LastFailure)

	_, err = svc.DueSessionReports(t.Context(), next, service.MaxReconcileLimit+1)
	require.Error(t, err)
}

// TestAReportOfOnlyClosedSessionsIsRefused: a payment reported for an id whose
// every session is canceled or declined, while the provider holds the money,
// is money taken on a closed session, refused with its own code so the
// checkout says so; one whose money went back, or whose session an order
// captured, is a repeated report, taken with nothing to do.
func TestAReportOfOnlyClosedSessionsIsRefused(t *testing.T) {
	page := newPayingPageProvider("paytr")
	page.inspection = coreprovider.SessionInspection{Status: coreprovider.SessionCaptured, CapturedAmount: amount}
	svc, store := reconcileFixture(t, page)
	seedPending(t, store, "ses_canceled", "paytr", time.Hour)
	seedPending(t, store, "ses_declined", "paytr", time.Hour)
	seedPending(t, store, "ses_captured", "paytr", time.Hour)
	withExternalID(store, "ses_canceled", "OID1", models.SessionCanceled)
	withExternalID(store, "ses_declined", "OID1", models.SessionFailed)
	withExternalID(store, "ses_captured", "OID2", models.SessionCaptured)

	ids, err := svc.RecordSessionReport(t.Context(), "paytr", "OID1")

	require.Error(t, err)
	assert.Equal(t, service.CodeReportedSessionClosed, coreerrors.CodeOf(err))
	assert.Empty(t, ids)
	assert.Empty(t, store.reports, "no report is opened for a closed session")

	page.inspection = coreprovider.SessionInspection{
		Status: coreprovider.SessionCanceled, CapturedAmount: amount, RefundedAmount: amount,
	}
	ids, err = svc.RecordSessionReport(t.Context(), "paytr", "OID1")
	require.NoError(t, err, "a report sent again after the refund holds nothing")
	assert.Empty(t, ids)

	ids, err = svc.RecordSessionReport(t.Context(), "paytr", "OID2")
	require.NoError(t, err)
	assert.Empty(t, ids)

	ids, err = svc.RecordSessionReport(t.Context(), "paytr", "UNKNOWN")
	require.NoError(t, err, "an id that names nothing is not a closed session")
	assert.Empty(t, ids)
}

// TestASettledReportIsNotNamedAgain: a report the checkout settled — given up,
// say — is not handed back to it when the provider reports the session again,
// and a new report is first due a retry after it was opened.
func TestASettledReportIsNotNamedAgain(t *testing.T) {
	svc, store := reconcileFixture(t, newPayingPageProvider("paytr"))
	seedPending(t, store, "ses_given_up", "paytr", time.Hour)
	seedPending(t, store, "ses_open", "paytr", time.Hour)
	withExternalID(store, "ses_given_up", "OID1", models.SessionPending)
	withExternalID(store, "ses_open", "OID1", models.SessionPending)
	before := time.Now().UTC()

	ids, err := svc.RecordSessionReport(t.Context(), "paytr", "OID1")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ses_given_up", "ses_open"}, ids)
	report, err := svc.SessionReport(t.Context(), "ses_open")
	require.NoError(t, err)
	assert.WithinDuration(t, before.Add(models.ReportFirstRetry), report.NextAttemptAt, time.Minute,
		"the checkout attempts it when it arrives; the sweep takes it a retry later")

	require.NoError(t, svc.SettleSessionReport(t.Context(), "ses_given_up", models.ReportGaveUp, "no stock"))
	ids, err = svc.RecordSessionReport(t.Context(), "paytr", "OID1")

	require.NoError(t, err)
	assert.Equal(t, []string{"ses_open"}, ids)
}

// TestMoneyTakenOnACanceledSessionIsReported: a customer who paid on a page
// whose session was canceled meanwhile leaves money no order holds; the
// reconciliation's second page reads canceled sessions too, reports that one,
// agrees with one given back, and discovery opens no report for either.
func TestMoneyTakenOnACanceledSessionIsReported(t *testing.T) {
	page := newPayingPageProvider("paytr")
	svc, store := reconcileFixture(t, page)
	seedPending(t, store, "ses_canceled", "paytr", time.Hour)
	withExternalID(store, "ses_canceled", "OID1", models.SessionCanceled)

	page.inspection = coreprovider.SessionInspection{Status: coreprovider.SessionCaptured, CapturedAmount: amount}
	report, err := svc.Reconcile(t.Context(), 15*time.Minute, 50)
	require.NoError(t, err)
	require.Len(t, report.Divergences, 1)
	assert.Equal(t, "ses_canceled", report.Divergences[0].SessionID)

	found, err := svc.DiscoverReportedSessions(t.Context(), time.Now().UTC())
	require.NoError(t, err)
	assert.Empty(t, found, "a canceled session completes no cart")

	page.inspection = coreprovider.SessionInspection{
		Status: coreprovider.SessionCanceled, CapturedAmount: amount, RefundedAmount: amount,
	}
	report, err = svc.Reconcile(t.Context(), 15*time.Minute, 50)
	require.NoError(t, err)
	assert.Empty(t, report.Divergences, "a payment given back is not money held")
}
