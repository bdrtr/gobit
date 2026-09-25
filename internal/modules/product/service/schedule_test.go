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

// in returns a moment the given distance from the clock.
func in(d time.Duration) *time.Time {
	at := scheduleClock.Add(d)
	return &at
}

// pass moves a stored product's moments into the past, which is how a test
// makes time go by for the store.
func pass(store *memStore, id string) {
	p := store.products[id]
	if p.PublishAt != nil {
		p.PublishAt = in(-2 * time.Minute)
	}
	if p.ArchiveAt != nil {
		p.ArchiveAt = in(-time.Minute)
	}
	store.products[id] = p
}

// TestADraftCanBeScheduledAndIsPublishedAtItsMoment walks the life of a
// publication moment (ADR 0177): set on a draft, the draft stays a draft, and
// the pass after the moment publishes it with the event a publication by hand
// gets.
func TestADraftCanBeScheduledAndIsPublishedAtItsMoment(t *testing.T) {
	svc, store, bus := newScheduleService(t)
	ctx := context.Background()
	draft := seedDraft(t, svc, "launch")

	scheduled, err := svc.SetSchedule(ctx, draft.ID, service.Schedule{PublishAt: in(time.Hour)})
	require.NoError(t, err)
	assert.Equal(t, models.StatusDraft, scheduled.Status, "a scheduled product is still a draft")
	require.NotNil(t, scheduled.PublishAt)

	published, archived, err := svc.ApplyDueSchedules(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, published, "nothing is due before the moment")
	assert.Empty(t, archived)

	pass(store, draft.ID)
	before := len(bus.events())

	published, _, err = svc.ApplyDueSchedules(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, []string{draft.ID}, published)
	assert.Equal(t, models.StatusPublished, store.products[draft.ID].Status)
	assert.Nil(t, store.products[draft.ID].PublishAt, "a published product carries no publication moment")

	events := bus.events()[before:]
	require.Len(t, events, 1)
	assert.Equal(t, service.EventProductUpdated, events[0].Name)
	assert.Equal(t, "published", events[0].Data[service.EventFieldStatus],
		"the index and the webhooks get what a publication by hand gives them")
}

// TestALimitedTimeProductArrivesAndLeaves verifies the pair (ADR 0179): a draft
// scheduled to arrive and to leave, with both moments passed at once, is
// published and then archived in one pass, with the event of each, in order.
func TestALimitedTimeProductArrivesAndLeaves(t *testing.T) {
	svc, store, bus := newScheduleService(t)
	ctx := context.Background()
	draft := seedDraft(t, svc, "drop")

	_, err := svc.SetSchedule(ctx, draft.ID, service.Schedule{PublishAt: in(time.Hour), ArchiveAt: in(48 * time.Hour)})
	require.NoError(t, err)
	pass(store, draft.ID)
	before := len(bus.events())

	published, archived, err := svc.ApplyDueSchedules(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, []string{draft.ID}, published)
	assert.Equal(t, []string{draft.ID}, archived)

	stored := store.products[draft.ID]
	assert.Equal(t, models.StatusArchived, stored.Status)
	assert.Nil(t, stored.PublishAt)
	assert.Nil(t, stored.ArchiveAt, "an archived product carries no schedule")

	events := bus.events()[before:]
	require.Len(t, events, 2)
	assert.Equal(t, "published", events[0].Data[service.EventFieldStatus])
	assert.Equal(t, "archived", events[1].Data[service.EventFieldStatus],
		"what happened to it, in the order it happened")
}

// TestAPublishedProductCanBeScheduledToLeave verifies the moment to leave on a
// product that is already live.
func TestAPublishedProductCanBeScheduledToLeave(t *testing.T) {
	svc, store, _ := newScheduleService(t)
	ctx := context.Background()
	live := seedProduct(t, svc, "seasonal", "Seasonal")

	scheduled, err := svc.SetSchedule(ctx, live.ID, service.Schedule{ArchiveAt: in(time.Hour)})
	require.NoError(t, err)
	assert.Equal(t, models.StatusPublished, scheduled.Status, "it stays live until the moment")

	pass(store, live.ID)
	_, archived, err := svc.ApplyDueSchedules(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, []string{live.ID}, archived)
}

// TestAScheduleIsRefusedWhereItWouldMeanNothing verifies the refusals.
func TestAScheduleIsRefusedWhereItWouldMeanNothing(t *testing.T) {
	svc, _, _ := newScheduleService(t)
	ctx := context.Background()
	live := seedProduct(t, svc, "live", "Live")
	draft := seedDraft(t, svc, "later")
	gone := seedProductInput(t, svc, service.CreateProductInput{
		Handle: "gone", Title: "Gone", Status: models.StatusArchived,
	})
	now := scheduleClock

	for name, tc := range map[string]struct {
		id       string
		schedule service.Schedule
		conflict bool
		code     string
	}{
		"a publication moment on a published product": {live.ID,
			service.Schedule{PublishAt: in(time.Hour)}, true, service.CodeNotADraft},
		"a moment to leave on an archived product": {gone.ID,
			service.Schedule{ArchiveAt: in(time.Hour)}, true, service.CodeAlreadyArchived},
		"a moment that is now": {draft.ID,
			service.Schedule{PublishAt: &now}, false, service.CodeScheduleNotAhead},
		"a moment in the past": {draft.ID,
			service.Schedule{ArchiveAt: in(-time.Minute)}, false, service.CodeScheduleNotAhead},
		"leaving before arriving": {draft.ID,
			service.Schedule{PublishAt: in(2 * time.Hour), ArchiveAt: in(time.Hour)}, false,
			service.CodeScheduleOutOfOrder},
		"no moment at all": {draft.ID, service.Schedule{}, false, service.CodeScheduleEmpty},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.SetSchedule(ctx, tc.id, tc.schedule)
			require.Error(t, err)
			if tc.conflict {
				assert.True(t, errors.IsConflict(err), "error: %v", err)
			} else {
				assert.True(t, errors.IsInvalid(err), "error: %v", err)
			}
			assert.Equal(t, tc.code, errors.CodeOf(err))
		})
	}
}

// TestAStatusChangeByHandSpendsWhatItOvertakes verifies what a hand change
// leaves of a schedule: publishing spends the publication moment and keeps the
// moment to leave; archiving spends both; clearing takes both off.
func TestAStatusChangeByHandSpendsWhatItOvertakes(t *testing.T) {
	svc, _, _ := newScheduleService(t)
	ctx := context.Background()

	byHand := seedDraft(t, svc, "by-hand")
	_, err := svc.SetSchedule(ctx, byHand.ID, service.Schedule{PublishAt: in(time.Hour), ArchiveAt: in(2 * time.Hour)})
	require.NoError(t, err)
	published := models.StatusPublished
	updated, err := svc.UpdateProduct(ctx, byHand.ID, service.UpdateProductInput{Status: &published})
	require.NoError(t, err)
	assert.Nil(t, updated.PublishAt, "a product published by hand is not published a second time later")
	assert.NotNil(t, updated.ArchiveAt, "its moment to leave still stands")

	archivedStatus := models.StatusArchived
	updated, err = svc.UpdateProduct(ctx, byHand.ID, service.UpdateProductInput{Status: &archivedStatus})
	require.NoError(t, err)
	assert.Nil(t, updated.ArchiveAt, "an archived product has nothing left to leave")

	cleared := seedDraft(t, svc, "cleared")
	_, err = svc.SetSchedule(ctx, cleared.ID, service.Schedule{PublishAt: in(time.Hour), ArchiveAt: in(2 * time.Hour)})
	require.NoError(t, err)
	back, err := svc.ClearSchedule(ctx, cleared.ID)
	require.NoError(t, err)
	assert.Nil(t, back.PublishAt)
	assert.Nil(t, back.ArchiveAt)
	assert.Equal(t, models.StatusDraft, back.Status, "clearing a schedule does not publish or archive")
}

// TestTheReadLayerCarriesTheSchedule verifies that the product record the admin
// panel reads carries both moments, and nil for a product without them
// (ADR 0178, ADR 0179).
func TestTheReadLayerCarriesTheSchedule(t *testing.T) {
	svc, store, _ := newScheduleService(t)
	ctx := context.Background()
	draft := seedDraft(t, svc, "launch-read")
	arrive, leave := in(time.Hour), in(2*time.Hour)
	_, err := svc.SetSchedule(ctx, draft.ID, service.Schedule{PublishAt: arrive, ArchiveAt: leave})
	require.NoError(t, err)
	live := seedProduct(t, svc, "live-read", "Live")

	provider := service.NewProductProvider(store)
	records, err := provider.List(ctx, query.ListOptions{Fields: []string{"id", "publish_at", "archive_at"}})
	require.NoError(t, err)

	byID := map[string]query.Record{}
	for _, record := range records {
		id, _ := record["id"].(string)
		byID[id] = record
	}
	assert.Equal(t, *arrive, byID[draft.ID]["publish_at"], "the scheduled draft's moments are on its record")
	assert.Equal(t, *leave, byID[draft.ID]["archive_at"])
	assert.Nil(t, byID[live.ID]["publish_at"], "a product with no schedule carries nil, not the zero time")
	assert.Nil(t, byID[live.ID]["archive_at"])
}
