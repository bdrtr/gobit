package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// issuedCard issues a card of the amount through the service.
func issuedCard(t *testing.T, svc *service.Service, amount int64) models.GiftCard {
	t.Helper()

	issued, err := svc.IssueGiftCard(context.Background(), service.IssueGiftCardInput{
		CurrencyCode: "TRY", Amount: amount, Reason: "a test",
	})
	require.NoError(t, err)

	return issued.Card
}

// TestAClosedCardHoldsNothing is ADR 0213: closing a card voids what it held,
// under the card's lock, and records when and why.
func TestAClosedCardHoldsNothing(t *testing.T) {
	t.Parallel()

	svc, store := giftCardService(t)
	card := issuedCard(t, svc, 7_000)

	closed, err := svc.DisableGiftCard(context.Background(), card.ID, "  sold by mistake ")

	require.NoError(t, err)
	require.NotNil(t, closed.Card.DisabledAt)
	assert.Equal(t, "sold by mistake", closed.Card.DisableReason)
	assert.Zero(t, closed.Balance)
	entries := store.giftEntries[card.ID]
	require.Len(t, entries, 2)
	assert.Equal(t, models.GiftCardVoid, entries[1].Kind)
	assert.Equal(t, int64(-7_000), entries[1].Amount)
	assert.Empty(t, entries[1].Reference, "a void belongs to no payment session")
	assert.Equal(t, []string{card.ID}, store.giftLocks, "the close took the card's lock")
	read, err := svc.GetGiftCard(context.Background(), card.ID)
	require.NoError(t, err)
	assert.Zero(t, read.Balance)
}

// TestClosingAClosedCardWritesNothing: a second close returns the card as the
// first left it.
func TestClosingAClosedCardWritesNothing(t *testing.T) {
	t.Parallel()

	svc, store := giftCardService(t)
	card := issuedCard(t, svc, 7_000)
	first, err := svc.DisableGiftCard(context.Background(), card.ID, "sold by mistake")
	require.NoError(t, err)

	second, err := svc.DisableGiftCard(context.Background(), card.ID, "another reason")

	require.NoError(t, err)
	assert.Equal(t, first.Card, second.Card, "the first reason stands")
	assert.Len(t, store.giftEntries[card.ID], 2, "one void")
}

// TestACardAPaymentHoldsIsNotClosed: a hold released after the close would
// put a balance back on a card that pays nothing.
func TestACardAPaymentHoldsIsNotClosed(t *testing.T) {
	t.Parallel()

	svc, store := giftCardService(t)
	card := issuedCard(t, svc, 7_000)
	store.giftHolds = map[string]int64{card.ID: 1}

	_, err := svc.DisableGiftCard(context.Background(), card.ID, "sold by mistake")

	require.Error(t, err)
	assert.Equal(t, service.CodeGiftCardHeld, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindConflict))
	assert.Len(t, store.giftEntries[card.ID], 1, "nothing was voided")
	assert.Nil(t, store.giftCards[card.ID].DisabledAt)
}

// TestAnEmptyCardIsClosedWithoutAVoid: there is nothing to take away, and a
// row of zero is not a row.
func TestAnEmptyCardIsClosedWithoutAVoid(t *testing.T) {
	t.Parallel()

	svc, store := giftCardService(t)
	card := issuedCard(t, svc, 7_000)
	store.giftEntries[card.ID] = append(store.giftEntries[card.ID], models.GiftCardEntry{
		ID: "gcentry_spent", GiftCardID: card.ID, Amount: -7_000, Kind: models.GiftCardHold, Reference: "gcses_1",
	})

	closed, err := svc.DisableGiftCard(context.Background(), card.ID, "spent and closed")

	require.NoError(t, err)
	assert.NotNil(t, closed.Card.DisabledAt)
	assert.Len(t, store.giftEntries[card.ID], 2, "no void")
}

// TestAClosedCardNeedsAReason as an issued one does.
func TestAClosedCardNeedsAReason(t *testing.T) {
	t.Parallel()

	svc, store := giftCardService(t)
	card := issuedCard(t, svc, 7_000)

	_, err := svc.DisableGiftCard(context.Background(), card.ID, "   ")

	assert.True(t, errors.IsInvalid(err))
	assert.Nil(t, store.giftCards[card.ID].DisabledAt)
}

// TestAClosedCardGetsNoNewCode: a new code would open nothing.
func TestAClosedCardGetsNoNewCode(t *testing.T) {
	t.Parallel()

	svc, store := giftCardService(t)
	card := issuedCard(t, svc, 7_000)
	_, err := svc.DisableGiftCard(context.Background(), card.ID, "sold by mistake")
	require.NoError(t, err)
	digest := store.giftDigests[card.ID]

	_, err = svc.ReplaceGiftCardCode(context.Background(), card.ID)

	require.Error(t, err)
	assert.Equal(t, service.CodeGiftCardClosed, errors.CodeOf(err))
	assert.Equal(t, digest, store.giftDigests[card.ID])
}
