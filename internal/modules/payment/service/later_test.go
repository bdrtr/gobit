package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/offline"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// TestAnOfflineMethodCapturesLater is what the checkout asks before it places
// an order (ADR 0284): an offline method's money comes later, a card's does
// not, and a provider that is not registered is refused as the tender check
// refuses it.
func TestAnOfflineMethodCapturesLater(t *testing.T) {
	transfer, err := offline.New("bank_transfer")
	require.NoError(t, err)
	svc, _ := reconcileFixture(t, newFakeProvider(providerID), transfer)

	later, err := svc.CapturesLater(transfer.ID())
	require.NoError(t, err)
	assert.True(t, later)

	later, err = svc.CapturesLater(providerID)
	require.NoError(t, err)
	assert.False(t, later, "a provider that says nothing captures at the checkout")

	_, err = svc.CapturesLater("cash_on_delivery")
	require.Error(t, err)
	assert.Equal(t, service.CodeProviderNotFound, coreerrors.CodeOf(err))
}

// TestReconcileLeavesOutAProviderWhoseMoneyComesLater is ADR 0284's other
// half: an offline session stays authorized until the customer pays, which is
// the provider working and not a capture in flight. It is neither examined nor
// counted as unverified, and with a page of one the card session beside it —
// newer, so behind it in the oldest-first listing — is still the one asked.
func TestReconcileLeavesOutAProviderWhoseMoneyComesLater(t *testing.T) {
	card := newInspectingProvider(providerID)
	transfer, err := offline.New("bank_transfer")
	require.NoError(t, err)
	svc, store := reconcileFixture(t, card, transfer)
	seedAuthorized(t, store, "payses_transfer", transfer.ID(), 72*time.Hour)
	seedAuthorized(t, store, "payses_card", providerID, time.Hour)

	report, err := svc.Reconcile(context.Background(), settled, 1)
	require.NoError(t, err)

	assert.Equal(t, 1, report.Examined)
	assert.Equal(t, 1, report.Agreed)
	assert.Equal(t, 1, card.inspectCalls, "the card's session is the one asked")
	assert.Zero(t, report.Unaskable, "the offline session is not counted as unverified")
	assert.False(t, report.Truncated, "the offline session does not fill the page")
	assert.True(t, report.Clean())
}
