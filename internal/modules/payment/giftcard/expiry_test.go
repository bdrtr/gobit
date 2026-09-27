package giftcard_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/giftcard"
)

// expire gives a card a moment by the given offset from now.
func (m *memStore) expire(id string, in time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	card := m.cards[id]
	at := time.Now().Add(in)
	card.ExpiresAt = &at
	m.cards[id] = card
}

// TestAnExpiredCardOpensNoPayment is ADR 0214: from its moment the card is
// refused, before the expiry job has closed it.
func TestAnExpiredCardOpensNoPayment(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.issue("gcard_1", testCode, "TRY", 10_000)
	store.expire("gcard_1", -time.Second)
	p := newProvider(store)

	_, err := p.CreateSession(context.Background(), coreprovider.CreateSessionInput{
		Amount: 1_000, CurrencyCode: "TRY", Reference: "paycol_1", IdempotencyKey: "k1",
		Data: map[string]any{giftcard.DataCode: testCode},
	})

	require.Error(t, err)
	assert.Equal(t, giftcard.CodeExpired, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindConflict))
}

// TestACardBeforeItsMomentPays: the moment is not a day early.
func TestACardBeforeItsMomentPays(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.issue("gcard_1", testCode, "TRY", 10_000)
	store.expire("gcard_1", time.Hour)
	p := newProvider(store)

	session(t, p, "k1", testCode, 1_000)
}

// TestARefundOntoAnExpiredCardIsRefused as onto a closed one: the expiry job
// would void it.
func TestARefundOntoAnExpiredCardIsRefused(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.issue("gcard_1", testCode, "TRY", 10_000)
	p := newProvider(store)
	opened := session(t, p, "k1", testCode, 4_000)
	_, err := p.Authorize(context.Background(), opened.ID)
	require.NoError(t, err)
	require.NoError(t, p.Capture(context.Background(), opened.ID, 0))
	store.expire("gcard_1", -time.Second)

	err = p.Refund(context.Background(), opened.ID, 1_000)

	require.Error(t, err)
	assert.Equal(t, giftcard.CodeExpired, errors.CodeOf(err))
}
