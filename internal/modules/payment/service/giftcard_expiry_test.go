package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// serviceWithValidity builds a service whose installation validity is the
// given number of days.
func serviceWithValidity(t *testing.T, days int) (*service.Service, *fakeStore) {
	t.Helper()

	store := newFakeStore()
	svc, err := service.New(service.Options{
		Store: store, Providers: service.NewProviderRegistry(), Events: newFakeBus(), GiftCardValidityDays: days,
	})
	require.NoError(t, err)

	return svc, store
}

// TestAnOperatorNamesACardsMoment is ADR 0214: the moment given is the card's.
func TestAnOperatorNamesACardsMoment(t *testing.T) {
	t.Parallel()

	svc, _ := serviceWithValidity(t, 30)
	at := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)

	issued, err := svc.IssueGiftCard(context.Background(), service.IssueGiftCardInput{
		CurrencyCode: "TRY", Amount: 1_000, Reason: "a test", ExpiresAt: &at,
	})

	require.NoError(t, err)
	require.NotNil(t, issued.Card.ExpiresAt)
	assert.Equal(t, at, *issued.Card.ExpiresAt)
}

// TestAMomentBehindIsRefused: a card that would be born expired is a typo.
func TestAMomentBehindIsRefused(t *testing.T) {
	t.Parallel()

	svc, store := serviceWithValidity(t, 0)
	at := time.Now().Add(-time.Minute)

	_, err := svc.IssueGiftCard(context.Background(), service.IssueGiftCardInput{
		CurrencyCode: "TRY", Amount: 1_000, Reason: "a test", ExpiresAt: &at,
	})

	assert.True(t, errors.IsInvalid(err), "%v", err)
	assert.Empty(t, store.giftCards)
}

// TestTheInstallationsValidityReachesEveryCard: an issued card with no moment
// and every sold card are made with the installation's validity.
func TestTheInstallationsValidityReachesEveryCard(t *testing.T) {
	t.Parallel()

	svc, store := serviceWithValidity(t, 365)

	issued, err := svc.IssueGiftCard(context.Background(), service.IssueGiftCardInput{
		CurrencyCode: "TRY", Amount: 1_000, Reason: "a test",
	})
	require.NoError(t, err)
	sold, _, err := svc.IssueSoldGiftCard(context.Background(), service.SoldGiftCardInput{
		Reference: "oline_1:1", OrderID: "order_1", CurrencyCode: "TRY", Amount: 1_000,
	})
	require.NoError(t, err)

	assert.Equal(t, []int32{365, 365}, store.giftValidity)
	for _, card := range []models.GiftCard{issued.Card, sold.Card} {
		require.NotNil(t, card.ExpiresAt)
		assert.Equal(t, card.CreatedAt.AddDate(0, 0, 365), *card.ExpiresAt)
	}
}

// TestTheValidityIsBounded: a negative validity would read as never, and past
// the ceiling a shop means never.
func TestTheValidityIsBounded(t *testing.T) {
	t.Parallel()

	for _, days := range []int{-1, service.MaxGiftCardValidityDays + 1} {
		_, err := service.New(service.Options{
			Store: newFakeStore(), Providers: service.NewProviderRegistry(), Events: newFakeBus(),
			GiftCardValidityDays: days,
		})
		assert.Error(t, err, "%d days", days)
	}
}

// TestExpiredCardsAreClosedAndHeldOnesWait: the pass closes what expired, as an
// operator's close does, and leaves a card a payment holds for a later pass.
func TestExpiredCardsAreClosedAndHeldOnesWait(t *testing.T) {
	t.Parallel()

	svc, store := serviceWithValidity(t, 0)
	past := time.Now().Add(-time.Minute)
	var ids []string
	for range 3 {
		issued, err := svc.IssueGiftCard(context.Background(), service.IssueGiftCardInput{
			CurrencyCode: "TRY", Amount: 1_000, Reason: "a test",
		})
		require.NoError(t, err)
		ids = append(ids, issued.Card.ID)
	}
	for _, id := range ids[:2] {
		card := store.giftCards[id]
		card.ExpiresAt = &past
		store.giftCards[id] = card
	}
	store.giftHolds = map[string]int64{ids[1]: 1}

	closed, held, err := svc.ExpireGiftCards(context.Background(), 100)

	require.NoError(t, err)
	assert.Equal(t, []int{1, 1}, []int{closed, held})
	expired := store.giftCards[ids[0]]
	require.NotNil(t, expired.DisabledAt)
	assert.Equal(t, service.ReasonExpired, expired.DisableReason)
	assert.Equal(t, models.GiftCardVoid, store.giftEntries[ids[0]][1].Kind)
	assert.Nil(t, store.giftCards[ids[1]].DisabledAt, "the held card waits")
	assert.Nil(t, store.giftCards[ids[2]].DisabledAt, "a card with no moment never expires")
}

// TestTheInteropCarriesACardsMoment: the sale flow mails what the interop
// hands it, so the moment has to cross it, and a card that never expires
// crosses as empty.
func TestTheInteropCarriesACardsMoment(t *testing.T) {
	t.Parallel()

	for _, days := range []int{0, 30} {
		svc, store := serviceWithValidity(t, days)
		interop := service.NewInterop(svc)

		cardID, code, expiresAt, err := interop.IssueSoldGiftCard(context.Background(),
			"oline_1:1", "order_1", "TRY", 1_000)

		require.NoError(t, err)
		assert.NotEmpty(t, code)
		card := store.giftCards[cardID]
		if days == 0 {
			assert.Empty(t, expiresAt, "a card that never expires")
			continue
		}
		require.NotNil(t, card.ExpiresAt)
		assert.Equal(t, card.ExpiresAt.UTC().Format(time.RFC3339), expiresAt)
	}
}
