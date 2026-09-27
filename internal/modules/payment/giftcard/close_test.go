package giftcard_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/giftcard"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// TestAClosedCardOpensNoPayment is ADR 0213: the code of a closed card is
// answered as closed, before an order is placed and when a session is opened.
func TestAClosedCardOpensNoPayment(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.issue("gcard_1", testCode, "TRY", 10_000)
	store.close("gcard_1")
	p := newProvider(store)
	in := coreprovider.CreateSessionInput{
		Amount: 1_000, CurrencyCode: "TRY", Reference: "paycol_1", IdempotencyKey: "k1",
		Data: map[string]any{giftcard.DataCode: testCode},
	}

	checked := p.CheckPayment(context.Background(), in)
	_, opened := p.CreateSession(context.Background(), in)

	for _, err := range []error{checked, opened} {
		require.Error(t, err)
		assert.Equal(t, giftcard.CodeDisabled, errors.CodeOf(err))
		assert.True(t, errors.HasKind(err, errors.KindConflict))
	}
}

// TestARefundOntoAClosedCardIsRefused: the card pays nothing and holds
// nothing, so the money would reach nobody; the refund is refused under the
// card's lock and nothing is written.
func TestARefundOntoAClosedCardIsRefused(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.issue("gcard_1", testCode, "TRY", 10_000)
	p := newProvider(store)
	opened := session(t, p, "k1", testCode, 4_000)
	_, err := p.Authorize(context.Background(), opened.ID)
	require.NoError(t, err)
	require.NoError(t, p.Capture(context.Background(), opened.ID, 0))
	store.close("gcard_1")
	store.locked = nil

	err = p.Refund(context.Background(), opened.ID, 1_000)

	require.Error(t, err)
	assert.Equal(t, giftcard.CodeDisabled, errors.CodeOf(err))
	assert.Equal(t, []string{"gcard_1"}, store.locked, "the card's lock was taken before it was read")
	for _, entry := range store.entries {
		assert.NotEqual(t, models.GiftCardRefund, entry.Kind, "no refund row")
	}
	sessionAfter, err := store.GiftCardSession(context.Background(), opened.ID)
	require.NoError(t, err)
	assert.Zero(t, sessionAfter.RefundedAmount)
}

// TestARefundOntoAnOpenCardIsWritten: the close's check stands aside for a
// card nobody closed.
func TestARefundOntoAnOpenCardIsWritten(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.issue("gcard_1", testCode, "TRY", 10_000)
	p := newProvider(store)
	opened := session(t, p, "k1", testCode, 4_000)
	_, err := p.Authorize(context.Background(), opened.ID)
	require.NoError(t, err)
	require.NoError(t, p.Capture(context.Background(), opened.ID, 0))

	require.NoError(t, p.Refund(context.Background(), opened.ID, 1_000))

	balance, err := store.GiftCardBalance(context.Background(), "gcard_1")
	require.NoError(t, err)
	assert.Equal(t, int64(7_000), balance)
}
