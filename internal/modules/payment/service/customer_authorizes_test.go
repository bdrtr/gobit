package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// payingPageProvider is a provider whose customer pays on its own page, as
// PayTR's does, and which reports on its sessions (ADR 0438).
type payingPageProvider struct{ *inspectingProvider }

// CustomerAuthorizes says the customer pays on the provider's page.
func (payingPageProvider) CustomerAuthorizes() bool { return true }

// newPayingPageProvider builds one that reports nothing paid.
func newPayingPageProvider(id string) payingPageProvider {
	p := newInspectingProvider(id)
	p.inspection = coreprovider.SessionInspection{Status: coreprovider.SessionPending}

	return payingPageProvider{p}
}

// seedPending writes a pending session straight into the store, as the
// opening of a payment leaves it.
func seedPending(
	t *testing.T, store *fakeStore, id, providerID string, age time.Duration,
) models.PaymentSession {
	t.Helper()

	ses := seedAuthorized(t, store, id, providerID, age)
	ses.Status, ses.AuthorizedAmount = models.SessionPending, 0

	store.mu.Lock()
	defer store.mu.Unlock()
	store.sessions[ses.ID] = ses

	return ses
}

// TestAProviderSaysWhetherItsCustomerAuthorizes: the capability is asked by a
// type assertion, a provider without it does not, and an unregistered one is
// refused.
func TestAProviderSaysWhetherItsCustomerAuthorizes(t *testing.T) {
	svc, _ := reconcileFixture(t, newPayingPageProvider("paytr"), newInspectingProvider(providerID))

	page, err := svc.CustomerAuthorizes("paytr")
	require.NoError(t, err)
	assert.True(t, page)

	driven, err := svc.CustomerAuthorizes(providerID)
	require.NoError(t, err)
	assert.False(t, driven)

	_, err = svc.CustomerAuthorizes("nobody")
	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "%v", err)

	assert.True(t, service.ProviderCustomerAuthorizes(newPayingPageProvider("paytr")))
	assert.False(t, service.ProviderCustomerAuthorizes(newFakeProvider(providerID)))
}

// TestAReportedStatusIsTheProvidersOwn: the provider's ledger answers, and a
// provider whose sessions this module drives is refused.
func TestAReportedStatusIsTheProvidersOwn(t *testing.T) {
	page := newPayingPageProvider("paytr")
	page.inspection = coreprovider.SessionInspection{Status: coreprovider.SessionCaptured, CapturedAmount: amount}
	svc, store := reconcileFixture(t, page, newInspectingProvider(providerID))
	paid := seedPending(t, store, "ses_paid", "paytr", time.Minute)
	driven := seedPending(t, store, "ses_driven", providerID, time.Minute)

	status, err := svc.ReportedStatus(t.Context(), paid.ID)
	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionCaptured, status)
	assert.Equal(t, 1, page.inspectCalls)

	_, err = svc.ReportedStatus(t.Context(), driven.ID)
	require.Error(t, err)
	assert.Equal(t, service.CodeSessionNotReportable, coreerrors.CodeOf(err))

	_, err = svc.ReportedStatus(t.Context(), "ses_nobody")
	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "%v", err)
}

// TestACustomerAuthorizingProviderThatCannotBeAskedIsRefused: without an
// inspector there is no ledger to read, and silence is not a report.
func TestACustomerAuthorizingProviderThatCannotBeAskedIsRefused(t *testing.T) {
	svc, store := reconcileFixture(t, &blindPayingPage{newFakeProvider("blind")})
	ses := seedPending(t, store, "ses_blind", "blind", time.Minute)

	_, err := svc.ReportedStatus(t.Context(), ses.ID)

	require.Error(t, err)
	assert.Equal(t, service.CodeSessionNotReportable, coreerrors.CodeOf(err))
}

// blindPayingPage is a provider whose customer pays on its page and which
// cannot be inspected.
type blindPayingPage struct{ *fakeProvider }

// CustomerAuthorizes says the customer pays on the provider's page.
func (*blindPayingPage) CustomerAuthorizes() bool { return true }

// TestReconciliationReportsAPaidSessionWithNoOrder: the customer paid on the
// provider's page and no completion came, so the session is still pending here
// while the provider says captured (ADR 0438).
func TestReconciliationReportsAPaidSessionWithNoOrder(t *testing.T) {
	page := newPayingPageProvider("paytr")
	page.inspection = coreprovider.SessionInspection{Status: coreprovider.SessionCaptured, CapturedAmount: amount}
	svc, store := reconcileFixture(t, page)
	ses := seedPending(t, store, "ses_paid", "paytr", time.Hour)

	report, err := svc.Reconcile(t.Context(), settled, 50)

	require.NoError(t, err)
	assert.Equal(t, 1, report.Examined)
	require.Len(t, report.Divergences, 1)
	assert.Equal(t, service.Divergence{
		SessionID: ses.ID, CollectionID: ses.PaymentCollectionID, ProviderID: "paytr", ExternalID: ses.ExternalID,
		LocalStatus: models.SessionPending, LocalAuthorized: 0,
		ProviderStatus: coreprovider.SessionCaptured, ProviderCaptured: amount, CurrencyCode: currency,
	}, report.Divergences[0])
}

// TestAPaymentGivenBackIsNotMoneyWithNoOrder: a pending session the provider
// took and refunded whole keeps nothing an order is missing, so the pending
// page counts it as agreement (ADR 0438).
func TestAPaymentGivenBackIsNotMoneyWithNoOrder(t *testing.T) {
	page := newPayingPageProvider("paytr")
	page.inspection = coreprovider.SessionInspection{
		Status: coreprovider.SessionCanceled, CapturedAmount: amount, RefundedAmount: amount,
	}
	svc, store := reconcileFixture(t, page)
	seedPending(t, store, "ses_refunded", "paytr", time.Hour)

	report, err := svc.Reconcile(t.Context(), settled, 50)

	require.NoError(t, err)
	assert.Equal(t, 1, report.Agreed)
	assert.Empty(t, report.Divergences)
}

// TestReconciliationAsksOnlyAboutTheRightPendingSessions: a pending session of
// a driven provider holds no money, and one inside the settling window is a
// completion on its way.
func TestReconciliationAsksOnlyAboutTheRightPendingSessions(t *testing.T) {
	page := newPayingPageProvider("paytr")
	driven := newInspectingProvider(providerID)
	svc, store := reconcileFixture(t, page, driven)
	seedPending(t, store, "ses_driven", providerID, time.Hour)
	seedPending(t, store, "ses_fresh", "paytr", time.Minute)
	seedPending(t, store, "ses_unpaid", "paytr", time.Hour)

	report, err := svc.Reconcile(t.Context(), settled, 50)

	require.NoError(t, err)
	assert.Equal(t, 1, report.Examined, "only the settled pending session of the paying-page provider")
	assert.Equal(t, 1, report.Agreed, "a page nobody paid on is agreement")
	assert.Empty(t, report.Divergences)
	assert.Equal(t, 1, page.inspectCalls)
	assert.Zero(t, driven.inspectCalls)
}

// TestThePendingPageIsItsOwn: abandoned pages pile up and stay pending, so
// they never take the authorized sessions' page, and the caller's limit, a
// budget for that page, does not cut theirs.
func TestThePendingPageIsItsOwn(t *testing.T) {
	page := newPayingPageProvider("paytr")
	card := newInspectingProvider(providerID)
	svc, store := reconcileFixture(t, page, card)
	seedAuthorized(t, store, "ses_card_a", providerID, 5*time.Hour)
	seedAuthorized(t, store, "ses_card_b", providerID, 4*time.Hour)
	seedPending(t, store, "ses_old", "paytr", 3*time.Hour)
	seedPending(t, store, "ses_mid", "paytr", 2*time.Hour)
	seedPending(t, store, "ses_new", "paytr", time.Hour)
	page.inspection = coreprovider.SessionInspection{Status: coreprovider.SessionCaptured, CapturedAmount: amount}

	report, err := svc.Reconcile(t.Context(), settled, 2)

	require.NoError(t, err)
	assert.Equal(t, 5, report.Examined, "two authorized and the three pending, each page its own")
	assert.Equal(t, 2, card.inspectCalls, "the pending sessions did not crowd the authorized page")
	var asked []string
	for _, divergence := range report.Divergences {
		asked = append(asked, divergence.SessionID)
	}
	assert.ElementsMatch(t, []string{"ses_new", "ses_mid", "ses_old"}, asked)
	assert.False(t, report.Truncated)
	assert.False(t, report.UnreportedTruncated)
}

// TestThePendingPageReadsTheLastSettledDay: a paid page is asked about for the
// day after it settled, and past it the page no longer carries it.
func TestThePendingPageReadsTheLastSettledDay(t *testing.T) {
	page := newPayingPageProvider("paytr")
	page.inspection = coreprovider.SessionInspection{Status: coreprovider.SessionCaptured, CapturedAmount: amount}
	svc, store := reconcileFixture(t, page)
	seedPending(t, store, "ses_inside", "paytr", service.UnreportedWindow)
	seedPending(t, store, "ses_outside", "paytr", service.UnreportedWindow+settled+time.Minute)

	report, err := svc.Reconcile(t.Context(), settled, 50)

	require.NoError(t, err)
	assert.Equal(t, 1, report.Examined)
	require.Len(t, report.Divergences, 1)
	assert.Equal(t, "ses_inside", report.Divergences[0].SessionID)
}

// TestAPageThatCannotCoverAnIntervalIsReported: more pages settled within one
// interval than the page holds leaves some never asked, and that is reported
// rather than read in part in silence.
func TestAPageThatCannotCoverAnIntervalIsReported(t *testing.T) {
	page := newPayingPageProvider("paytr")
	svc, store := reconcileFixture(t, page)
	for i := range service.MaxReconcileLimit + 1 {
		seedPending(t, store, fmt.Sprintf("ses_%04d", i), "paytr", 20*time.Minute+time.Duration(i)*time.Second)
	}

	report, err := svc.Reconcile(t.Context(), settled, 50)

	require.NoError(t, err)
	assert.True(t, report.UnreportedTruncated)
	assert.False(t, report.Truncated, "the authorized page was not full")
	assert.Equal(t, service.MaxReconcileLimit, report.Examined)
	assert.Equal(t, service.MaxReconcileLimit, page.inspectCalls)
	assert.False(t, report.Clean(), "a session never asked is a gap in what only this page looks for")
}

// TestADayLargerThanThePageThatCoversAnIntervalIsNotReported: the page still
// reaches back past one interval, so every session met at least its first
// pass, and a warning every pass would be one an operator learns to skip.
func TestADayLargerThanThePageThatCoversAnIntervalIsNotReported(t *testing.T) {
	page := newPayingPageProvider("paytr")
	svc, store := reconcileFixture(t, page)
	for i := range service.MaxReconcileLimit + 1 {
		seedPending(t, store, fmt.Sprintf("ses_%04d", i), "paytr", 20*time.Minute+time.Duration(i)*time.Minute)
	}

	report, err := svc.Reconcile(t.Context(), settled, 50)

	require.NoError(t, err)
	assert.False(t, report.UnreportedTruncated)
	assert.Equal(t, service.MaxReconcileLimit, report.Examined)
	assert.True(t, report.Clean())
}

// TestTheCheckoutReadsTheSessionAsJSON: the interop's answer carries what the
// checkout holds a completion to — the cart and the customer the collection
// was opened for, the provider, the amounts and the provider's data — under the
// names the checkout decodes (ADR 0438).
func TestTheCheckoutReadsTheSessionAsJSON(t *testing.T) {
	svc, _ := reconcileFixture(t, newPayingPageProvider("paytr"))
	col, err := svc.CreatePaymentCollection(t.Context(), service.CreateCollectionInput{
		Reference: "cart_1", CustomerID: "cus_1", CurrencyCode: currency, Amount: amount,
	})
	require.NoError(t, err)
	opened, err := svc.CreateSession(t.Context(), col.ID, "paytr", service.CreateSessionInput{IdempotencyKey: "k1"})
	require.NoError(t, err)

	raw, err := service.NewInterop(svc).CheckoutSessionJSON(t.Context(), opened.ID)

	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, opened.ID, got["id"])
	assert.Equal(t, "paytr", got["provider_id"])
	assert.Equal(t, "pending", got["status"])
	assert.EqualValues(t, amount, got["amount"])
	assert.Equal(t, currency, got["currency_code"])
	collection, ok := got["collection"].(map[string]any)
	require.True(t, ok, "%s", raw)
	assert.Equal(t, col.ID, collection["id"])
	assert.Equal(t, "cart_1", collection["reference"])
	assert.Equal(t, "cus_1", collection["customer_id"])
	assert.EqualValues(t, amount, collection["amount"])
	assert.Equal(t, currency, collection["currency_code"])
	assert.EqualValues(t, 0, collection["captured_amount"])
}

// TestCheckoutSessionReadsTheSessionAndItsCollection: what the checkout holds
// a completion to.
func TestCheckoutSessionReadsTheSessionAndItsCollection(t *testing.T) {
	svc, _ := reconcileFixture(t, newPayingPageProvider("paytr"))
	col, err := svc.CreatePaymentCollection(t.Context(), service.CreateCollectionInput{
		Reference: "cart_1", CurrencyCode: currency, Amount: amount,
	})
	require.NoError(t, err)
	opened, err := svc.CreateSession(t.Context(), col.ID, "paytr", service.CreateSessionInput{IdempotencyKey: "k1"})
	require.NoError(t, err)

	ses, read, err := svc.CheckoutSession(context.Background(), opened.ID)

	require.NoError(t, err)
	assert.Equal(t, opened.ID, ses.ID)
	assert.Equal(t, col.ID, read.ID)
	assert.Equal(t, "cart_1", read.Reference)
	assert.Equal(t, amount, read.Amount)
}
