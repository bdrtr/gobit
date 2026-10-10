package service_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// An event's moment is the stored moment (ADR 0437, D278): a parcel's
// cancellation, delivery and coming back each carry the stamp the row was
// written with, which keeps microseconds, and not the clock's nanoseconds.

// subMicrosecondClock reads 999 nanoseconds past a microsecond, so a moment
// taken from it differs from the stored one.
var subMicrosecondClock = testNow.Add(999 * time.Nanosecond)

// newSetupWithClock is [newSetup] with the given clock.
func newSetupWithClock(t *testing.T, clock time.Time) testSetup {
	t.Helper()

	setup := newSetup(t)
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(setup.provider))
	svc, err := service.New(service.Options{
		Store: setup.store, Providers: registry, DispatchBound: setup.bound, Events: setup.events,
		Clock: func() time.Time { return clock },
	})
	require.NoError(t, err)
	setup.svc = svc

	return setup
}

// requireStoredMoment asserts the one published event and its outbox row carry
// the stamp the row holds, at full precision.
func requireStoredMoment(t *testing.T, setup testSetup, field string, stored *time.Time) {
	t.Helper()

	require.NotNil(t, stored)
	require.NotEqual(t, subMicrosecondClock, *stored, "the premise: the row keeps less than the clock")
	require.Len(t, setup.events.published, 1)
	rows := setup.store.outboxRows()
	require.Len(t, rows, 1)
	for where, data := range map[string]map[string]any{"publish": setup.events.published[0].Data, "outbox": rows[0].data} {
		text, ok := data[field].(string)
		require.Truef(t, ok, "the %s carries %s as a string", where, field)
		moment, err := time.Parse(time.RFC3339Nano, text)
		require.NoError(t, err)
		assert.Truef(t, moment.Equal(*stored), "the %s's %s is %s and the row holds %s",
			where, field, moment, stored)
	}
}

// TestACanceledParcelCarriesTheStoredMoment: fulfillment.canceled's
// canceled_at is the row's.
func TestACanceledParcelCarriesTheStoredMoment(t *testing.T) {
	t.Parallel()

	setup := newSetupWithClock(t, subMicrosecondClock)
	ful := createTestFulfillment(t, setup)

	require.NoError(t, setup.svc.CancelFulfillment(t.Context(), ful.ID))

	got, err := setup.svc.GetFulfillment(t.Context(), ful.ID)
	require.NoError(t, err)
	require.Equal(t, models.StatusCanceled, got.Status)
	requireStoredMoment(t, setup, service.EventFieldCanceledAt, got.CanceledAt)
}

// TestADeliveredParcelCarriesTheStoredMoment: fulfillment.delivered's
// delivered_at is the row's.
func TestADeliveredParcelCarriesTheStoredMoment(t *testing.T) {
	t.Parallel()

	setup := newSetupWithClock(t, subMicrosecondClock)
	ful := createTestFulfillment(t, setup)

	delivered, err := setup.svc.MarkDelivered(t.Context(), ful.ID)
	require.NoError(t, err)

	requireStoredMoment(t, setup, service.EventFieldDeliveredAt, delivered.DeliveredAt)
}

// TestAParcelThatCameBackCarriesTheStoredMoment: fulfillment.returned's
// returned_at is the row's.
func TestAParcelThatCameBackCarriesTheStoredMoment(t *testing.T) {
	t.Parallel()

	setup := newSetupWithClock(t, subMicrosecondClock)
	ful := createTestFulfillment(t, setup)
	_, err := setup.svc.MarkShipped(t.Context(), ful.ID, ful.TrackingNumber, "")
	require.NoError(t, err)

	back, err := setup.svc.MarkReturned(t.Context(), ful.ID)
	require.NoError(t, err)

	requireStoredMoment(t, setup, service.EventFieldReturnedAt, back.ReturnedAt)
}
