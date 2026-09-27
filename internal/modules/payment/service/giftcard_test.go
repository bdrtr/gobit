package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// giftCardService builds a service over a fake store.
func giftCardService(t *testing.T) (*service.Service, *fakeStore) {
	t.Helper()

	store := newFakeStore()
	svc, err := service.New(service.Options{
		Store: store, Providers: service.NewProviderRegistry(), Events: newFakeBus(),
	})
	require.NoError(t, err)

	return svc, store
}

// TestAGiftCardIsIssuedWithItsBalanceAndACodeShownOnce is ADR 0208's issue:
// the card, its issue row and the code, which the shop keeps only as a digest.
func TestAGiftCardIsIssuedWithItsBalanceAndACodeShownOnce(t *testing.T) {
	t.Parallel()

	svc, store := giftCardService(t)

	issued, err := svc.IssueGiftCard(context.Background(), service.IssueGiftCardInput{
		CurrencyCode: " try ", Amount: 25_000, Reason: "compensation for a late parcel",
	})
	require.NoError(t, err)

	assert.Equal(t, "TRY", issued.Card.CurrencyCode)
	assert.Equal(t, int64(25_000), issued.Balance)
	normalized, ok := models.NormalizeGiftCardCode(issued.Code)
	require.True(t, ok)
	assert.Equal(t, models.GiftCardCodeDigest(normalized), store.giftDigests[issued.Card.ID],
		"the card is found again by the digest of its code")
	assert.Equal(t, models.GiftCardCodeTail(normalized), issued.Card.CodeTail)
	require.Len(t, store.giftEntries[issued.Card.ID], 1)
	assert.Equal(t, models.GiftCardIssue, store.giftEntries[issued.Card.ID][0].Kind)

	read, err := svc.GetGiftCard(context.Background(), issued.Card.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(25_000), read.Balance)
}

// TestAGiftCardIssueIsOneTransaction: a card whose balance could not be written
// is not left behind without one.
func TestAGiftCardIssueIsOneTransaction(t *testing.T) {
	t.Parallel()

	svc, store := giftCardService(t)
	store.failGiftEntry = errors.Internal("payment_query_failed", "the ledger is down")

	_, err := svc.IssueGiftCard(context.Background(), service.IssueGiftCardInput{
		CurrencyCode: "TRY", Amount: 1_000, Reason: "a test",
	})
	require.Error(t, err)

	assert.Empty(t, store.giftCards, "the card went with its balance")
}

// TestAGiftCardIssueRefusesWhatItCannotKeep: a currency that is not one, an
// amount that is not positive, and no reason.
func TestAGiftCardIssueRefusesWhatItCannotKeep(t *testing.T) {
	t.Parallel()

	svc, store := giftCardService(t)
	for name, in := range map[string]service.IssueGiftCardInput{
		"no currency":   {Amount: 1_000, Reason: "a test"},
		"zero":          {CurrencyCode: "TRY", Reason: "a test"},
		"negative":      {CurrencyCode: "TRY", Amount: -1, Reason: "a test"},
		"above the cap": {CurrencyCode: "TRY", Amount: models.MaxAmount + 1, Reason: "a test"},
		"no reason":     {CurrencyCode: "TRY", Amount: 1_000, Reason: "  "},
	} {
		_, err := svc.IssueGiftCard(context.Background(), in)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), name)
	}
	assert.Empty(t, store.giftCards)
}

// TestTheGiftCardsAreListedWithTheirBalances lists them newest first.
func TestTheGiftCardsAreListedWithTheirBalances(t *testing.T) {
	t.Parallel()

	svc, _ := giftCardService(t)
	var ids []string
	for _, amount := range []int64{1_000, 2_000, 3_000} {
		issued, err := svc.IssueGiftCard(context.Background(), service.IssueGiftCardInput{
			CurrencyCode: "TRY", Amount: amount, Reason: "a test",
		})
		require.NoError(t, err)
		ids = append(ids, issued.Card.ID)
	}

	listed, total, err := svc.ListGiftCards(context.Background(), service.Page{Limit: 2})
	require.NoError(t, err)

	assert.Equal(t, int64(3), total)
	require.Len(t, listed, 2)
	balances := map[string]int64{}
	for _, card := range listed {
		balances[card.Card.ID] = card.Balance
	}
	assert.Equal(t, map[string]int64{ids[2]: 3_000, ids[1]: 2_000}, balances)

	entries, count, err := svc.ListGiftCardEntries(context.Background(), ids[0], service.Page{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	assert.Equal(t, int64(1_000), entries[0].Amount)

	_, _, err = svc.ListGiftCardEntries(context.Background(), "gcard_missing", service.Page{})
	assert.True(t, errors.IsNotFound(err))
}

// checkingProvider records what the tender check hands it.
type checkingProvider struct {
	*fakeProvider
	got    coreprovider.CreateSessionInput
	answer error
}

func (p *checkingProvider) CheckPayment(_ context.Context, in coreprovider.CreateSessionInput) error {
	p.got = in

	return p.answer
}

// TestTheTenderCheckHandsTheProviderThePayment: the currency and the data the
// payment will carry reach the provider's check, which is how a gift card code
// is refused before an order exists (ADR 0208).
func TestTheTenderCheckHandsTheProviderThePayment(t *testing.T) {
	t.Parallel()

	provider := &checkingProvider{
		fakeProvider: newFakeProvider("coded"),
		answer:       errors.Invalid("coded_unknown", "no such code"),
	}
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(provider))
	svc, err := service.New(service.Options{Store: newFakeStore(), Providers: registry, Events: newFakeBus()})
	require.NoError(t, err)

	err = svc.CheckTender(context.Background(), "coded", "cus_1", "TRY", map[string]any{"code": "X"})

	assert.Equal(t, "coded_unknown", errors.CodeOf(err))
	assert.Equal(t, coreprovider.CreateSessionInput{
		CurrencyCode: "TRY", CustomerID: "cus_1", Data: map[string]any{"code": "X"},
	}, provider.got)
}
