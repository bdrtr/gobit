package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// The fixture's own constants.
//
// They are declared HERE rather than borrowed from the module's older Turkish
// test fixtures: this file is new, so it is English (ADR 0012), and reaching
// across for `amount` would have put a Turkish identifier into it.
const (
	// settled is a window comfortably wider than anything the tests backdate to.
	settled = 15 * time.Minute
	// providerID, amount and currency describe the sessions under test.
	providerID = "manual"
	amount     = int64(10_000)
	currency   = "TRY"
)

// inspectingProvider is a fake provider that CAN be asked about a session.
//
// It is a separate type from [fakeProvider] on purpose, so that the plain fake
// keeps standing for the other half of the contract: a provider with no
// inspector must be counted as unaskable, and a fake that quietly grew the
// method would delete that test's subject.
type inspectingProvider struct {
	*fakeProvider

	// inspection is what the provider claims about every session.
	inspection coreprovider.SessionInspection
	// inspectErr, when set, is returned instead.
	inspectErr error
	// inspectCalls counts the round trips, which is how a test proves a session
	// inside the settling window was never asked about at all.
	inspectCalls int
}

// The inspecting fake meets the optional contract at compile time.
var _ coreprovider.SessionInspector = (*inspectingProvider)(nil)

// newInspectingProvider builds a provider that reports nothing captured.
func newInspectingProvider(id string) *inspectingProvider {
	return &inspectingProvider{
		fakeProvider: newFakeProvider(id),
		inspection:   coreprovider.SessionInspection{Status: coreprovider.SessionAuthorized},
	}
}

// InspectSession returns the scripted view.
func (p *inspectingProvider) InspectSession(
	_ context.Context, _ string,
) (coreprovider.SessionInspection, error) {
	p.inspectCalls++
	if p.inspectErr != nil {
		return coreprovider.SessionInspection{}, p.inspectErr
	}
	return p.inspection, nil
}

// reconcileFixture wires a service over a set of providers.
func reconcileFixture(t *testing.T, providers ...coreprovider.PaymentProvider) (
	*service.Service, *fakeStore,
) {
	t.Helper()

	store := newFakeStore()
	registry := service.NewProviderRegistry()
	for _, p := range providers {
		require.NoError(t, registry.Register(p))
	}

	svc, err := service.New(service.Options{Store: store, Providers: registry})
	require.NoError(t, err)

	return svc, store
}

// seedAuthorized writes an authorized session straight into the store.
//
// It bypasses the service deliberately. The state this job exists for cannot be
// produced through the service at all — it is what a ROLLED BACK transaction
// leaves behind, so any path that writes it correctly writes something else.
func seedAuthorized(
	t *testing.T, store *fakeStore, id, providerID string, age time.Duration,
) models.PaymentSession {
	t.Helper()

	ses := models.PaymentSession{
		ID:                  id,
		PaymentCollectionID: "paycol_" + id,
		ProviderID:          providerID,
		ExternalID:          "ext_" + id,
		Status:              models.SessionAuthorized,
		Amount:              amount,
		AuthorizedAmount:    amount,
		CurrencyCode:        currency,
		IdempotencyKey:      "idem_" + id,
		CreatedAt:           time.Now().UTC().Add(-age),
		UpdatedAt:           time.Now().UTC().Add(-age),
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	store.sessions[ses.ID] = ses

	return ses
}

// TestReconcileReportsACapturedSessionTheModuleHasNoRecordOf is the case the
// whole capability exists for: the provider took the money, the local
// transaction rolled back, and no local record disagrees with any other.
func TestReconcileReportsACapturedSessionTheModuleHasNoRecordOf(t *testing.T) {
	prov := newInspectingProvider(providerID)
	prov.inspection = coreprovider.SessionInspection{
		Status:           coreprovider.SessionCaptured,
		AuthorizedAmount: amount,
		CapturedAmount:   amount,
	}
	svc, store := reconcileFixture(t, prov)
	ses := seedAuthorized(t, store, "payses_lost", providerID, time.Hour)

	report, err := svc.Reconcile(context.Background(), settled, 50)
	require.NoError(t, err)

	require.Len(t, report.Divergences, 1)
	assert.False(t, report.Clean())
	assert.Equal(t, 0, report.Agreed)

	// BOTH sides are carried, and the external id with them: that is the value
	// an operator pastes into the provider's own dashboard.
	d := report.Divergences[0]
	assert.Equal(t, ses.ID, d.SessionID)
	assert.Equal(t, ses.PaymentCollectionID, d.CollectionID)
	assert.Equal(t, ses.ExternalID, d.ExternalID)
	assert.Equal(t, models.SessionAuthorized, d.LocalStatus)
	assert.Equal(t, amount, d.LocalAuthorized)
	assert.Equal(t, coreprovider.SessionCaptured, d.ProviderStatus)
	assert.Equal(t, amount, d.ProviderCaptured)
	assert.Equal(t, currency, d.CurrencyCode)
}

// TestReconcileWritesNothing is the decision, asserted rather than documented.
//
// A comparison that repaired what it found would be this module deciding, on
// its own and unwatched, that money moved (ADR 0017).
func TestReconcileWritesNothing(t *testing.T) {
	prov := newInspectingProvider(providerID)
	prov.inspection = coreprovider.SessionInspection{
		Status:         coreprovider.SessionCaptured,
		CapturedAmount: amount,
	}
	svc, store := reconcileFixture(t, prov)
	before := seedAuthorized(t, store, "payses_lost", providerID, time.Hour)

	writesBefore := store.sessionWrites
	_, err := svc.Reconcile(context.Background(), settled, 50)
	require.NoError(t, err)

	store.mu.Lock()
	after := store.sessions[before.ID]
	store.mu.Unlock()

	assert.Equal(t, writesBefore, store.sessionWrites)
	assert.Equal(t, before, after)
	assert.Empty(t, store.payments)
}

// TestReconcileAgreesWhenTheProviderTookNothing keeps the ordinary case quiet.
func TestReconcileAgreesWhenTheProviderTookNothing(t *testing.T) {
	prov := newInspectingProvider(providerID)
	svc, store := reconcileFixture(t, prov)
	seedAuthorized(t, store, "payses_ok", providerID, time.Hour)

	report, err := svc.Reconcile(context.Background(), settled, 50)
	require.NoError(t, err)

	assert.True(t, report.Clean())
	assert.Equal(t, 1, report.Agreed)
	assert.Empty(t, report.Divergences)
}

// TestReconcileNeverCallsAProviderInsideTheSettlingWindow proves the window is
// applied to the QUERY, not to the answer.
//
// A capture in flight sits in exactly the suspect state, so a pass that asked
// about it would report every ordinary payment as a discrepancy.
func TestReconcileNeverCallsAProviderInsideTheSettlingWindow(t *testing.T) {
	prov := newInspectingProvider(providerID)
	prov.inspection = coreprovider.SessionInspection{
		Status:         coreprovider.SessionCaptured,
		CapturedAmount: amount,
	}
	svc, store := reconcileFixture(t, prov)
	seedAuthorized(t, store, "payses_inflight", providerID, time.Minute)

	report, err := svc.Reconcile(context.Background(), settled, 50)
	require.NoError(t, err)

	assert.Equal(t, 0, prov.inspectCalls)
	assert.Equal(t, 0, report.Examined)
	assert.True(t, report.Clean())
}

// TestReconcileCountsAnUninspectableProviderApartFromAgreement is the reason
// [coreprovider.SessionInspector] is an optional interface rather than a method
// on every provider.
//
// "The two ledgers agree" and "nobody could ask" must never look the same. A
// forced method would have made every provider answer something, and the
// cheapest something to answer is zero.
func TestReconcileCountsAnUninspectableProviderApartFromAgreement(t *testing.T) {
	prov := newFakeProvider(providerID)
	svc, store := reconcileFixture(t, prov)
	seedAuthorized(t, store, "payses_blind", providerID, time.Hour)

	report, err := svc.Reconcile(context.Background(), settled, 50)
	require.NoError(t, err)

	assert.Equal(t, 1, report.Unaskable)
	assert.Equal(t, 0, report.Agreed)
	assert.False(t, report.Clean())
}

// TestReconcileCountsAnUnregisteredProviderAsUnaskable covers the provider that
// was uninstalled while its sessions stayed.
func TestReconcileCountsAnUnregisteredProviderAsUnaskable(t *testing.T) {
	prov := newInspectingProvider(providerID)
	svc, store := reconcileFixture(t, prov)
	seedAuthorized(t, store, "payses_gone", "removed_provider", time.Hour)

	report, err := svc.Reconcile(context.Background(), settled, 50)
	require.NoError(t, err)

	assert.Equal(t, 1, report.Unaskable)
	assert.Equal(t, 0, report.Agreed)
	assert.False(t, report.Clean())
}

// TestReconcileKeepsAskingAfterAProviderFails proves one unreachable provider
// does not blind the pass to the others.
//
// The sessions a failing provider would have covered are precisely the ones
// nobody else is looking at.
func TestReconcileKeepsAskingAfterAProviderFails(t *testing.T) {
	broken := newInspectingProvider("broken")
	broken.inspectErr = errors.New("connection refused")

	working := newInspectingProvider("working")
	working.inspection = coreprovider.SessionInspection{
		Status:         coreprovider.SessionCaptured,
		CapturedAmount: amount,
	}

	svc, store := reconcileFixture(t, broken, working)
	// The broken provider's session is OLDER, so it is listed first and the
	// pass has to survive it to reach the other.
	seedAuthorized(t, store, "payses_broken", "broken", 2*time.Hour)
	seedAuthorized(t, store, "payses_working", "working", time.Hour)

	report, err := svc.Reconcile(context.Background(), settled, 50)
	require.NoError(t, err)

	assert.Equal(t, 1, report.Unreachable)
	assert.Len(t, report.Divergences, 1)
	assert.Equal(t, "payses_working", report.Divergences[0].SessionID)
	assert.False(t, report.Clean())
}

// TestReconcileCountsADisownedSessionApartFromAFault holds the distinction the
// inspector's contract draws: a provider that has never heard of a session is
// reporting a fact about this installation, not a network blip.
func TestReconcileCountsADisownedSessionApartFromAFault(t *testing.T) {
	prov := newInspectingProvider(providerID)
	prov.inspectErr = coreerrors.NotFound("provider_session_missing", "no such session")

	svc, store := reconcileFixture(t, prov)
	seedAuthorized(t, store, "payses_disowned", providerID, time.Hour)

	report, err := svc.Reconcile(context.Background(), settled, 50)
	require.NoError(t, err)

	assert.Equal(t, 1, report.Unknown)
	assert.Equal(t, 0, report.Unreachable)
	assert.Equal(t, 0, report.Agreed)
	assert.False(t, report.Clean())
}

// TestReconcileReportsAFilledLimit keeps a truncated pass from reading as a
// complete one.
func TestReconcileReportsAFilledLimit(t *testing.T) {
	prov := newInspectingProvider(providerID)
	svc, store := reconcileFixture(t, prov)
	seedAuthorized(t, store, "payses_a", providerID, 3*time.Hour)
	seedAuthorized(t, store, "payses_b", providerID, 2*time.Hour)
	seedAuthorized(t, store, "payses_c", providerID, time.Hour)

	report, err := svc.Reconcile(context.Background(), settled, 2)
	require.NoError(t, err)

	assert.True(t, report.Truncated)
	assert.Equal(t, 2, report.Examined)

	full, err := svc.Reconcile(context.Background(), settled, 3)
	require.NoError(t, err)
	assert.False(t, full.Truncated)
	assert.Equal(t, 3, full.Examined)
}

// TestReconcileRejectsAnAbsentSettlingWindow refuses the input that would make
// every ordinary payment a finding.
func TestReconcileRejectsAnAbsentSettlingWindow(t *testing.T) {
	svc, _ := reconcileFixture(t, newInspectingProvider(providerID))

	_, err := svc.Reconcile(context.Background(), 0, 50)
	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))

	_, err = svc.Reconcile(context.Background(), settled, 0)
	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))

	// The upper bound is the provider's, not the database's: past it the pass
	// asks for more network calls than any deadline can hold.
	_, err = svc.Reconcile(context.Background(), settled, service.MaxReconcileLimit+1)
	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))
}
