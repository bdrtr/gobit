package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/offline"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// What the shop is still waiting for, and the operator recording that it came
// (ADR 0287).

// sessionCountingStore counts the batch reads of sessions, the one read the
// awaited field costs.
type sessionCountingStore struct {
	*fakeStore
	sessionReads int
}

func (c *sessionCountingStore) SessionsOfCollections(
	ctx context.Context, ids []string,
) ([]models.PaymentSession, error) {
	c.sessionReads++

	return c.fakeStore.SessionsOfCollections(ctx, ids)
}

// awaitingCase is a collection of 10,000 paid 4,000 by a card and 6,000 by a
// bank transfer, both authorized, over a store that counts its session reads.
type awaitingCase struct {
	svc          *service.Service
	store        *sessionCountingStore
	collectionID string
	cardID       string
	transferID   string
}

func awaitingFixture(t *testing.T) awaitingCase {
	t.Helper()

	transfer, err := offline.New("bank_transfer")
	require.NoError(t, err)
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider(refundProviderID)))
	require.NoError(t, registry.Register(transfer))
	c := awaitingCase{store: &sessionCountingStore{fakeStore: newFakeStore()}}
	c.svc, err = service.New(service.Options{Store: c.store, Providers: registry, Events: newFakeBus()})
	require.NoError(t, err)

	col, err := c.svc.CreatePaymentCollection(t.Context(), service.CreateCollectionInput{
		Reference: "cart_awaiting", Amount: 10_000, CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	open := func(providerID string, amount int64) string {
		ses, err := c.svc.CreateSession(t.Context(), col.ID, providerID, service.CreateSessionInput{
			Amount: amount, IdempotencyKey: "awaiting-" + providerID,
		})
		require.NoError(t, err)
		authorized, err := c.svc.AuthorizePayment(t.Context(), ses.ID)
		require.NoError(t, err)
		require.Equal(t, models.SessionAuthorized, authorized.Status)

		return ses.ID
	}
	c.collectionID = col.ID
	c.cardID = open(refundProviderID, 4_000)
	c.transferID = open(transfer.ID(), 6_000)

	return c
}

// awaitingOf reads one collection's awaited sessions through the provider.
func awaitingOf(t *testing.T, svc *service.Service, collectionID string) []map[string]any {
	t.Helper()

	records, err := service.NewQueryProvider(svc).FetchByIDs(t.Context(),
		[]string{collectionID}, []string{service.FieldID, service.FieldAwaiting})
	require.NoError(t, err)
	require.Len(t, records, 1)
	awaiting, ok := records[0][service.FieldAwaiting].([]map[string]any)
	require.True(t, ok, "awaiting is a list of records: %T", records[0][service.FieldAwaiting])

	return awaiting
}

// TestTheProviderNamesWhatTheShopAwaits: the transfer's session is awaited,
// with its method and its amount; the card's, authorized as well, is not —
// its money moves through the provider.
func TestTheProviderNamesWhatTheShopAwaits(t *testing.T) {
	c := awaitingFixture(t)

	assert.Equal(t, []map[string]any{{
		service.AwaitingSessionID:  c.transferID,
		service.AwaitingProviderID: "bank_transfer",
		service.AwaitingAmount:     int64(6_000),
	}}, awaitingOf(t, c.svc, c.collectionID))
}

// TestTheAwaitedSessionsAreReadOnlyWhenAskedFor is the movements' cost rule: a
// read that does not name the field reads no session, and a read that names no
// field gets it with every other field the entity offers.
func TestTheAwaitedSessionsAreReadOnlyWhenAskedFor(t *testing.T) {
	c := awaitingFixture(t)
	provider := service.NewQueryProvider(c.svc)

	records, err := provider.FetchByIDs(t.Context(), []string{c.collectionID},
		[]string{service.FieldID, service.FieldCapturedAmount})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.NotContains(t, records[0], service.FieldAwaiting)
	assert.Zero(t, c.store.sessionReads)

	all, err := provider.List(t.Context(), query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Contains(t, all[0], service.FieldAwaiting,
		"a read that names no field gets every field the entity offers")
	assert.Equal(t, 1, c.store.sessionReads)
}

// TestRecordingTheMoneyCapturesTheSessionWhole: the transfer's session is
// captured for what it promised, is no longer awaited, and a second record
// returns the first capture.
func TestRecordingTheMoneyCapturesTheSessionWhole(t *testing.T) {
	c := awaitingFixture(t)

	payment, err := c.svc.RecordReceived(t.Context(), c.transferID)
	require.NoError(t, err)
	assert.Equal(t, int64(6_000), payment.Amount)
	assert.Empty(t, awaitingOf(t, c.svc, c.collectionID), "nothing is awaited any more")

	again, err := c.svc.RecordReceived(t.Context(), c.transferID)
	require.NoError(t, err)
	assert.Equal(t, payment.ID, again.ID, "a second record is the first capture")
}

// TestACardsSessionIsNotRecordedAsReceived: an operator's word is not a
// capture of money that moves through a provider.
func TestACardsSessionIsNotRecordedAsReceived(t *testing.T) {
	c := awaitingFixture(t)

	_, err := c.svc.RecordReceived(t.Context(), c.cardID)
	require.Error(t, err)
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindConflict))
	assert.Equal(t, service.CodeSessionCapturesNow, coreerrors.CodeOf(err))

	col, err := c.svc.GetPaymentCollection(t.Context(), c.collectionID)
	require.NoError(t, err)
	assert.Zero(t, col.CapturedAmount, "nothing was captured")
}

// TestNothingIsAwaitedWithoutAnOfflineMethod: an installation that names no
// offline method answers an empty list, not a missing one, without reading a
// session.
func TestNothingIsAwaitedWithoutAnOfflineMethod(t *testing.T) {
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider(refundProviderID)))
	store := &sessionCountingStore{fakeStore: newFakeStore()}
	svc, err := service.New(service.Options{Store: store, Providers: registry, Events: newFakeBus()})
	require.NoError(t, err)
	col, err := svc.CreatePaymentCollection(t.Context(), service.CreateCollectionInput{
		Reference: "cart_plain", Amount: 1_000, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	awaiting := awaitingOf(t, svc, col.ID)

	assert.NotNil(t, awaiting)
	assert.Empty(t, awaiting)
	assert.Zero(t, store.sessionReads)
}
