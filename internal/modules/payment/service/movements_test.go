package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// Every capture and every refund, each with its own amount (ADR 0170).

// countingStore counts the movement reads, so a test can see that a read which
// did not ask for them did not pay for them.
type countingStore struct {
	*fakeStore
	movementReads int
}

func (c *countingStore) PaymentMovementsByCollectionIDs(
	ctx context.Context, ids []string,
) ([]models.PaymentMovement, error) {
	c.movementReads++

	return c.fakeStore.PaymentMovementsByCollectionIDs(ctx, ids)
}

// newMovementService wires a service over a counting store.
func newMovementService(t *testing.T) (*service.Service, *countingStore) {
	t.Helper()

	prov := newFakeProvider(refundProviderID)
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	store := &countingStore{fakeStore: newFakeStore()}
	svc, err := service.New(service.Options{Store: store, Providers: registry, Events: newFakeBus()})
	require.NoError(t, err)

	return svc, store
}

// movementsOf reads one collection's movements through the provider.
func movementsOf(t *testing.T, svc *service.Service, collectionID string) []map[string]any {
	t.Helper()

	records, err := service.NewQueryProvider(svc).FetchByIDs(t.Context(),
		[]string{collectionID}, []string{service.FieldID, service.FieldMovements})
	require.NoError(t, err)
	require.Len(t, records, 1)

	movements, ok := records[0][service.FieldMovements].([]map[string]any)
	require.True(t, ok, "movements is a list of records: %T", records[0][service.FieldMovements])

	return movements
}

// TestTheProviderReportsEveryMovementWithItsOwnAmount is the history the two
// moments cannot tell: a capture and two partial refunds are three movements,
// and each carries what IT moved, not the running total.
func TestTheProviderReportsEveryMovementWithItsOwnAmount(t *testing.T) {
	svc, _ := newMovementService(t)
	ctx := t.Context()
	collectionID := capturedCollection(t, svc, "movements")

	first, err := svc.RefundCollection(ctx, collectionID, 3_000, "first part")
	require.NoError(t, err)
	second, err := svc.RefundCollection(ctx, collectionID, 2_000, "second part")
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Len(t, second, 1)

	movements := movementsOf(t, svc, collectionID)

	require.Len(t, movements, 3)
	capture := movements[0]
	assert.Equal(t, models.MovementCapture, capture[service.MovementKind])
	assert.Equal(t, refundAmount, capture[service.MovementAmount])
	assert.Equal(t, capture[service.MovementID], capture[service.MovementPaymentID],
		"a capture's record IS its payment")

	byID := map[any]map[string]any{movements[1][service.MovementID]: movements[1],
		movements[2][service.MovementID]: movements[2]}
	for refund, amount := range map[string]int64{first[0].ID: 3_000, second[0].ID: 2_000} {
		movement, ok := byID[refund]
		require.True(t, ok, "refund %s is missing from the movements", refund)
		assert.Equal(t, models.MovementRefund, movement[service.MovementKind])
		assert.Equal(t, amount, movement[service.MovementAmount],
			"a refund's amount is its own, not the refunded total")
		assert.Equal(t, capture[service.MovementID], movement[service.MovementPaymentID],
			"a refund names the payment it went back through")
	}

	for i := 1; i < len(movements); i++ {
		previous, _ := movements[i-1][service.MovementAt].(time.Time)
		current, _ := movements[i][service.MovementAt].(time.Time)
		assert.False(t, current.Before(previous), "the movements come oldest first")
	}
}

// TestACollectionNoMoneyMovedOnAnswersAnEmptyList keeps "nothing moved" apart
// from "the field was not produced".
func TestACollectionNoMoneyMovedOnAnswersAnEmptyList(t *testing.T) {
	svc, _ := newMovementService(t)

	movements := movementsOf(t, svc, emptyCollection(t, svc))

	assert.NotNil(t, movements)
	assert.Empty(t, movements)
}

// TestTheMovementsAreReadOnlyWhenAskedFor is the cost rule the two moments
// already follow: a read that does not name the field issues no query for it.
func TestTheMovementsAreReadOnlyWhenAskedFor(t *testing.T) {
	svc, store := newMovementService(t)
	collectionID := capturedCollection(t, svc, "not asked")
	provider := service.NewQueryProvider(svc)

	records, err := provider.FetchByIDs(t.Context(), []string{collectionID},
		[]string{service.FieldID, service.FieldCapturedAmount})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.NotContains(t, records[0], service.FieldMovements)
	assert.Zero(t, store.movementReads)

	all, err := provider.List(t.Context(), query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Contains(t, all[0], service.FieldMovements,
		"a read that names no field gets every field the entity offers")
	assert.Equal(t, 1, store.movementReads)
}
