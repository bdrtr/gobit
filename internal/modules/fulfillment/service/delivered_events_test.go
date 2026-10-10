package service_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// A parcel's delivery is on the bus (ADR 0437): written into the outbox in the
// delivery's transaction, published after the commit, once.

// TestADeliveredParcelIsAnnounced: marking a shipped parcel delivered writes
// fulfillment.delivered into the outbox with the status and publishes the same
// event after the commit, naming the parcel, what it was opened for and the
// moment the row was written with, and nothing else.
func TestADeliveredParcelIsAnnounced(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := createTestFulfillment(t, setup)
	_, err := setup.svc.MarkShipped(t.Context(), ful.ID, "TRK-1", "")
	require.NoError(t, err)

	delivered, err := setup.svc.MarkDelivered(t.Context(), ful.ID)
	require.NoError(t, err)
	require.NotNil(t, delivered.DeliveredAt)

	require.Equal(t, []string{service.EventFulfillmentDelivered}, setup.events.names())
	published := setup.events.published[0]
	assert.Equal(t, service.EventFulfillmentDelivered+":"+ful.ID, published.ID,
		"the id is derived from the parcel, so the outbox's ON CONFLICT writes one row per parcel")
	assert.Equal(t, map[string]any{
		service.EventFieldFulfillmentID: ful.ID,
		service.EventFieldReference:     ful.Reference,
		service.EventFieldReturnID:      "",
		service.EventFieldDeliveredAt:   delivered.DeliveredAt.UTC().Format(time.RFC3339Nano),
	}, published.Data, "identities and the moment the row holds; no tracking number")

	rows := setup.store.outboxRows()
	require.Len(t, rows, 1,
		"the fake store refuses an outbox write outside a transaction, so a row here "+
			"proves it commits with the status")
	assert.Equal(t, published.ID, rows[0].id)
	assert.Equal(t, service.EventFulfillmentDelivered, rows[0].name)
	assert.Equal(t, published.Data, rows[0].data, "one builder for the row and the publish")
}

// TestAParcelDeliveredBeforeItsCollectionIsAnnouncedOnce: a delivery reported
// before any collection announces itself, and the collection report arriving
// after it records the tracking number and announces nothing.
func TestAParcelDeliveredBeforeItsCollectionIsAnnouncedOnce(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := createTestFulfillment(t, setup)

	_, err := setup.svc.MarkDelivered(t.Context(), ful.ID)
	require.NoError(t, err)
	late, err := setup.svc.MarkShipped(t.Context(), ful.ID, ful.TrackingNumber, "")
	require.NoError(t, err)
	require.Equal(t, models.StatusDelivered, late.Status)

	assert.Equal(t, []string{service.EventFulfillmentDelivered}, setup.events.names())
	assert.Len(t, setup.store.outboxRows(), 1)
}

// TestAParcelReportedDeliveredTwiceIsAnnouncedOnce: the second report finds the
// parcel delivered, moves nothing and announces nothing.
func TestAParcelReportedDeliveredTwiceIsAnnouncedOnce(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := createTestFulfillment(t, setup)

	_, err := setup.svc.MarkDelivered(t.Context(), ful.ID)
	require.NoError(t, err)
	_, err = setup.svc.MarkDelivered(t.Context(), ful.ID)
	require.NoError(t, err)

	assert.Equal(t, []string{service.EventFulfillmentDelivered}, setup.events.names())
	assert.Len(t, setup.store.outboxRows(), 1)
}

// TestARefusedDeliveryAnnouncesNothing: a canceled parcel and one that came
// back cannot be delivered, and neither refusal writes or publishes anything.
func TestARefusedDeliveryAnnouncesNothing(t *testing.T) {
	t.Parallel()

	t.Run("canceled", func(t *testing.T) {
		t.Parallel()

		setup := newSetup(t)
		ful := createTestFulfillment(t, setup)
		require.NoError(t, setup.svc.CancelFulfillment(t.Context(), ful.ID))
		before := len(setup.store.outboxRows())

		_, err := setup.svc.MarkDelivered(t.Context(), ful.ID)

		require.Error(t, err)
		assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
		assert.NotContains(t, setup.events.names(), service.EventFulfillmentDelivered)
		assert.Len(t, setup.store.outboxRows(), before, "the refusal writes no row")
	})

	t.Run("come back", func(t *testing.T) {
		t.Parallel()

		setup := newSetup(t)
		ful := createTestFulfillment(t, setup)
		_, err := setup.svc.MarkShipped(t.Context(), ful.ID, "TRK-1", "")
		require.NoError(t, err)
		_, err = setup.svc.MarkReturned(t.Context(), ful.ID)
		require.NoError(t, err)
		before := len(setup.store.outboxRows())

		_, err = setup.svc.MarkDelivered(t.Context(), ful.ID)

		require.Error(t, err)
		assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
		assert.NotContains(t, setup.events.names(), service.EventFulfillmentDelivered)
		assert.Len(t, setup.store.outboxRows(), before, "the refusal writes no row")
	})
}

// TestADeliveredReturnParcelNamesItsReturn: a parcel bringing a return back is
// delivered to the warehouse, and its event names the return.
func TestADeliveredReturnParcelNamesItsReturn(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := returnOption(t, setup)
	setup.bound.returnLines = map[string]int64{"oli_1": 1}
	ful, err := setup.svc.CreateFulfillment(t.Context(), returnParcel(optionID, "key-delivered-back", 1))
	require.NoError(t, err)

	_, err = setup.svc.MarkDelivered(t.Context(), ful.ID)
	require.NoError(t, err)

	require.Len(t, setup.events.published, 1)
	assert.Equal(t, "ret_A", setup.events.published[0].Data[service.EventFieldReturnID])
	rows := setup.store.outboxRows()
	require.Len(t, rows, 1)
	assert.Equal(t, "ret_A", rows[0].data[service.EventFieldReturnID])
}

// TestADeliveryWhoseEventCannotBeWrittenIsNotMade: the outbox refusing the row
// fails the report, and the parcel stays where it was.
func TestADeliveryWhoseEventCannotBeWrittenIsNotMade(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := createTestFulfillment(t, setup)
	setup.store.outboxErr = errors.New("the outbox is unreachable")

	_, err := setup.svc.MarkDelivered(t.Context(), ful.ID)
	require.Error(t, err)

	got, err := setup.svc.GetFulfillment(t.Context(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusPending, got.Status, "no promise, no delivery")
	assert.Nil(t, got.DeliveredAt)
	assert.Empty(t, setup.events.names())
}

// TestALostPublishDoesNotUndoTheDelivery: the bus failing after the commit
// leaves the parcel delivered and its outbox row for the relay.
func TestALostPublishDoesNotUndoTheDelivery(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := createTestFulfillment(t, setup)
	setup.events.err = errors.New("the bus is down")

	delivered, err := setup.svc.MarkDelivered(t.Context(), ful.ID)

	require.NoError(t, err, "the parcel IS delivered; the relay sends what the bus missed")
	assert.Equal(t, models.StatusDelivered, delivered.Status)
	rows := setup.store.outboxRows()
	require.Len(t, rows, 1)
	assert.Equal(t, service.EventFulfillmentDelivered, rows[0].name)
}
