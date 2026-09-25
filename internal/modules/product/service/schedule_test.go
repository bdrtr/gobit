package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// scheduleClock is the moment every schedule test is judged at.
var scheduleClock = time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)

// newScheduleService builds a service over the in-memory store with a fixed
// clock and a recording bus.
func newScheduleService(t *testing.T) (*service.Service, *memStore, *fakeBus) {
	t.Helper()

	store, bus := newMemStore(), newFakeBus()
	svc, err := service.New(service.Options{
		Repo: store, Links: newFakeLinker(), Events: bus,
		Now: func() time.Time { return scheduleClock },
	})
	require.NoError(t, err)

	return svc, store, bus
}

// seedDraft writes a draft product.
func seedDraft(t *testing.T, svc *service.Service, handle string) models.Product {
	t.Helper()

	return seedProductInput(t, svc, service.CreateProductInput{
		Handle: handle, Title: handle, Status: models.StatusDraft,
	})
}

// TestADraftCanBeScheduledAndIsPublishedAtItsMoment walks the whole life of a
// schedule (ADR 0177): set on a draft, the draft stays a draft, and the pass
// after the moment publishes it with the event a publication by hand gets.
func TestADraftCanBeScheduledAndIsPublishedAtItsMoment(t *testing.T) {
	svc, store, bus := newScheduleService(t)
	ctx := context.Background()
	draft := seedDraft(t, svc, "launch")
	moment := scheduleClock.Add(time.Hour)

	scheduled, err := svc.SchedulePublication(ctx, draft.ID, moment)
	require.NoError(t, err)
	assert.Equal(t, models.StatusDraft, scheduled.Status, "a scheduled product is still a draft")
	require.NotNil(t, scheduled.PublishAt)
	assert.True(t, scheduled.PublishAt.Equal(moment))

	published, err := svc.PublishDue(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, published, "nothing is due before the moment")

	// The moment passes: the store is asked about a later clock.
	store.products[draft.ID] = withPublishAt(store.products[draft.ID], scheduleClock.Add(-time.Minute))
	before := len(bus.events())

	published, err = svc.PublishDue(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, []string{draft.ID}, published)
	assert.Equal(t, models.StatusPublished, store.products[draft.ID].Status)
	assert.Nil(t, store.products[draft.ID].PublishAt, "a published product carries no schedule")

	events := bus.events()[before:]
	require.Len(t, events, 1)
	assert.Equal(t, service.EventProductUpdated, events[0].Name)
	assert.Equal(t, "published", events[0].Data[service.EventFieldStatus],
		"the index and the webhooks get what a publication by hand gives them")
}

// TestAScheduleIsRefusedWhereItWouldMeanNothing verifies the two refusals: a
// product that is not a draft, and a moment that is not ahead.
func TestAScheduleIsRefusedWhereItWouldMeanNothing(t *testing.T) {
	svc, _, _ := newScheduleService(t)
	ctx := context.Background()
	live := seedProduct(t, svc, "live", "Live")
	draft := seedDraft(t, svc, "later")

	_, err := svc.SchedulePublication(ctx, live.ID, scheduleClock.Add(time.Hour))
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "error: %v", err)
	assert.Equal(t, service.CodeNotADraft, errors.CodeOf(err))

	for name, moment := range map[string]time.Time{
		"now":         scheduleClock,
		"in the past": scheduleClock.Add(-time.Minute),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.SchedulePublication(ctx, draft.ID, moment)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "error: %v", err)
			assert.Equal(t, service.CodeScheduleNotAhead, errors.CodeOf(err))
		})
	}
}

// TestLeavingTheDraftStateTakesTheScheduleWithIt verifies that publishing a
// scheduled draft by hand, or canceling the schedule, leaves no moment behind.
func TestLeavingTheDraftStateTakesTheScheduleWithIt(t *testing.T) {
	svc, _, _ := newScheduleService(t)
	ctx := context.Background()

	byHand := seedDraft(t, svc, "by-hand")
	_, err := svc.SchedulePublication(ctx, byHand.ID, scheduleClock.Add(time.Hour))
	require.NoError(t, err)
	published := models.StatusPublished
	updated, err := svc.UpdateProduct(ctx, byHand.ID, service.UpdateProductInput{Status: &published})
	require.NoError(t, err)
	assert.Nil(t, updated.PublishAt, "a product published by hand is not published a second time later")

	canceled := seedDraft(t, svc, "canceled")
	_, err = svc.SchedulePublication(ctx, canceled.ID, scheduleClock.Add(time.Hour))
	require.NoError(t, err)
	back, err := svc.CancelPublication(ctx, canceled.ID)
	require.NoError(t, err)
	assert.Nil(t, back.PublishAt)
	assert.Equal(t, models.StatusDraft, back.Status, "canceling a schedule does not publish or archive")
}

// withPublishAt moves a stored product's moment, which is how a test makes time
// pass for the store.
func withPublishAt(product models.Product, at time.Time) models.Product {
	product.PublishAt = &at
	return product
}

// TestTheReadLayerCarriesTheSchedule verifies that the product record the admin
// panel reads carries a draft's moment, and nil for a product without one
// (ADR 0178).
func TestTheReadLayerCarriesTheSchedule(t *testing.T) {
	svc, store, _ := newScheduleService(t)
	ctx := context.Background()
	draft := seedDraft(t, svc, "launch-read")
	moment := scheduleClock.Add(time.Hour)
	_, err := svc.SchedulePublication(ctx, draft.ID, moment)
	require.NoError(t, err)
	live := seedProduct(t, svc, "live-read", "Live")

	provider := service.NewProductProvider(store)
	records, err := provider.List(ctx, query.ListOptions{Fields: []string{"id", "publish_at"}})
	require.NoError(t, err)

	byID := map[string]any{}
	for _, record := range records {
		id, _ := record["id"].(string)
		byID[id] = record["publish_at"]
	}
	assert.Equal(t, moment, byID[draft.ID], "the scheduled draft's moment is on its record")
	assert.Nil(t, byID[live.ID], "a product with no schedule carries nil, not the zero time")
}
