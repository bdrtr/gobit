package giftcard_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/giftcard"
)

// testCode is a code as a person would read it off a card.
const testCode = "ABCD-EFGH-JKMN-PQRS"

func newProvider(store *memStore) *giftcard.Provider {
	return giftcard.New(store, slog.New(slog.DiscardHandler))
}

// session opens a session for the given code and amount.
func session(t *testing.T, p *giftcard.Provider, key, code string, amount int64) coreprovider.Session {
	t.Helper()

	opened, err := p.CreateSession(context.Background(), coreprovider.CreateSessionInput{
		Amount: amount, CurrencyCode: "TRY", Reference: "paycol_1", IdempotencyKey: key,
		Data: map[string]any{giftcard.DataCode: code},
	})
	require.NoError(t, err)

	return opened
}

// TestACardIsSpentByItsCode is ADR 0208's tender: the code in the payment's
// data finds the card, as typed by a person, and the session holds the card's
// balance, not a customer's.
func TestACardIsSpentByItsCode(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.issue("gcard_1", testCode, "TRY", 10_000)
	p := newProvider(store)

	opened := session(t, p, "k1", "  abcd efgh-jkmn pqrs ", 6_000)
	result, err := p.Authorize(context.Background(), opened.ID)
	require.NoError(t, err)

	assert.Equal(t, coreprovider.SessionAuthorized, result.Status)
	balance, err := store.GiftCardBalance(context.Background(), "gcard_1")
	require.NoError(t, err)
	assert.Equal(t, int64(4_000), balance, "the hold is on the card")
	assert.Equal(t, []string{"gcard_1"}, store.locked, "the card's balance was locked before it was read")
}

// TestACardThatDoesNotCoverTheSessionDeclines: too small a balance is a
// decline, as for any balance tender.
func TestACardThatDoesNotCoverTheSessionDeclines(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.issue("gcard_1", testCode, "TRY", 1_000)
	p := newProvider(store)

	result, err := p.Authorize(context.Background(), session(t, p, "k1", testCode, 6_000).ID)
	require.NoError(t, err)

	assert.Equal(t, coreprovider.SessionFailed, result.Status)
	balance, err := store.GiftCardBalance(context.Background(), "gcard_1")
	require.NoError(t, err)
	assert.Equal(t, int64(1_000), balance)
}

// TestACodeThatOpensNoCardIsOneAnswer: a malformed code, an unissued one and
// none at all are refused alike, before any session exists; a guesser learns
// nothing from which it was.
func TestACodeThatOpensNoCardIsOneAnswer(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.issue("gcard_1", testCode, "TRY", 10_000)
	p := newProvider(store)

	for name, data := range map[string]map[string]any{
		"unissued":  {giftcard.DataCode: "ZZZZ-ZZZZ-ZZZZ-ZZZZ"},
		"malformed": {giftcard.DataCode: "ABCD-EFGH"},
		"not text":  {giftcard.DataCode: 42},
		"absent":    nil,
	} {
		t.Run(name, func(t *testing.T) {
			in := coreprovider.CreateSessionInput{
				Amount: 1_000, CurrencyCode: "TRY", Reference: "paycol_1", IdempotencyKey: name, Data: data,
			}
			checked := p.CheckPayment(context.Background(), in)
			require.Error(t, checked)
			assert.True(t, errors.IsInvalid(checked))
			assert.Equal(t, giftcard.CodeUnknown, errors.CodeOf(checked))
			assert.Contains(t, checked.Error(), "no gift card has this code")

			_, opened := p.CreateSession(context.Background(), in)
			assert.Equal(t, giftcard.CodeUnknown, errors.CodeOf(opened))
		})
	}
	assert.Empty(t, store.sessions, "no session was opened for a code that opens nothing")
}

// TestACardPaysOnlyInItsCurrency: a card in euros pays nothing of a payment in
// lira, and says so before a session exists.
func TestACardPaysOnlyInItsCurrency(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.issue("gcard_1", testCode, "EUR", 10_000)
	p := newProvider(store)

	err := p.CheckPayment(context.Background(), coreprovider.CreateSessionInput{
		CurrencyCode: "TRY", Data: map[string]any{giftcard.DataCode: testCode},
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, giftcard.CodeCurrency, errors.CodeOf(err))

	assert.NoError(t, p.CheckPayment(context.Background(), coreprovider.CreateSessionInput{
		CurrencyCode: "eur", CustomerID: "", Data: map[string]any{giftcard.DataCode: testCode},
	}), "a guest may pay with a card; nobody's customer id is asked for")
}

// TestACanceledSessionGivesTheCardItsBalanceBack: the release goes to the card
// the hold came from.
func TestACanceledSessionGivesTheCardItsBalanceBack(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.issue("gcard_1", testCode, "TRY", 10_000)
	p := newProvider(store)
	opened := session(t, p, "k1", testCode, 6_000)
	_, err := p.Authorize(context.Background(), opened.ID)
	require.NoError(t, err)

	require.NoError(t, p.Cancel(context.Background(), opened.ID))

	balance, err := store.GiftCardBalance(context.Background(), "gcard_1")
	require.NoError(t, err)
	assert.Equal(t, int64(10_000), balance)
}
