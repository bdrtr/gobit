package service_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestCancelingAParcelANNOUNCESIt is the module's first event.
//
// Until ADR 0139 this module published nothing, and a parcel being canceled was a
// status nobody outside could hear about. The units it had been holding were
// counted as gone by whatever had written them off, and no flow could put them
// back because no flow was told.
func TestCancelingAParcelANNOUNCESIt(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := createTestFulfillment(t, setup)

	require.NoError(t, setup.svc.CancelFulfillment(t.Context(), ful.ID))

	require.Equal(t, []string{"fulfillment.canceled"}, setup.events.names())

	published := setup.events.published[0]
	assert.Equal(t, "fulfillment.canceled:"+ful.ID, published.ID,
		"the id is derived from the parcel, so the outbox's ON CONFLICT writes one row")
	assert.Equal(t, ful.ID, published.Data["fulfillment_id"])
	assert.Equal(t, ful.Reference, published.Data["reference"])
	assert.NotEmpty(t, published.Data["canceled_at"])
}

// TestTheOutboxRowIsWrittenINSIDETheTransaction is the guarantee.
//
// The direct publish is the fast path and it may be lost. What may NOT be lost is
// the row, because a parcel canceled without its event is stock that stays
// deducted with nothing saying it should not be — so the row commits with the
// status or neither does.
func TestTheOutboxRowIsWrittenINSIDETheTransaction(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := createTestFulfillment(t, setup)

	require.NoError(t, setup.svc.CancelFulfillment(t.Context(), ful.ID))

	rows := setup.store.outboxRows()
	require.Len(t, rows, 1,
		"the fake store refuses an outbox write outside a transaction, so a row "+
			"here proves the real one committed with the status")
	assert.Equal(t, "fulfillment.canceled", rows[0].name)
	assert.Equal(t, ful.ID, rows[0].data["fulfillment_id"])
}

// TestASecondCancellationANNOUNCESNothing pins the idempotent shape.
//
// Canceling twice is not an error — the compensation of a saga does it — but the
// second call released no units, and telling a subscriber otherwise would make it
// recompute a window that did not move.
func TestASecondCancellationANNOUNCESNothing(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := createTestFulfillment(t, setup)

	require.NoError(t, setup.svc.CancelFulfillment(t.Context(), ful.ID))
	require.NoError(t, setup.svc.CancelFulfillment(t.Context(), ful.ID))

	assert.Equal(t, []string{"fulfillment.canceled"}, setup.events.names(),
		"the parcel was canceled once, so it is announced once")
	assert.Len(t, setup.store.outboxRows(), 1)
}

// TestAPublishThatFailsDoesNOTFailTheCancellation separates the two paths.
//
// The row is already committed and the relay will send it. Failing the request
// would undo a cancellation that succeeded, for a delivery that is coming anyway.
func TestAPublishThatFailsDoesNOTFailTheCancellation(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := createTestFulfillment(t, setup)
	setup.events.err = errors.New("the bus is down")

	require.NoError(t, setup.svc.CancelFulfillment(t.Context(), ful.ID))

	assert.Len(t, setup.store.outboxRows(), 1,
		"the outbox row is what covers the lost publish")

	got, err := setup.svc.GetFulfillment(t.Context(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusCanceled, got.Status)
}

// TestQuantitiesOfFulfillmentAnswersForACANCELEDParcel is the read the subscriber
// needs.
//
// It is the one thing CommittedQuantities will not do, and the difference is the
// reason both exist: that one asks what has left the building and skips a canceled
// parcel, this one asks what was in the box and is called BECAUSE it was canceled.
func TestQuantitiesOfFulfillmentAnswersForACANCELEDParcel(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := createTestFulfillment(t, setup)
	require.NoError(t, setup.svc.CancelFulfillment(t.Context(), ful.ID))

	held, err := setup.svc.QuantitiesOfFulfillment(t.Context(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{testLineID: 2}, held,
		"a canceled parcel still answers what it was holding")

	committed, err := setup.svc.CommittedQuantities(t.Context(), []string{ful.ID})
	require.NoError(t, err)
	assert.Empty(t, committed[testLineID],
		"and the LIVE answer drops it, which is what makes the two separate reads")
}

// TestQuantitiesOfAnUnknownParcelIsEmptyRatherThanNotFound covers the subscriber's
// position.
//
// It is acting on an event about a parcel that may be gone, and a typed error
// would only be logged and dropped.
func TestQuantitiesOfAnUnknownParcelIsEmptyRatherThanNotFound(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)

	held, err := setup.svc.QuantitiesOfFulfillment(t.Context(), "ful_nothing")

	require.NoError(t, err)
	assert.Empty(t, held)
}

// TestAModuleWithNoBusWritesNoOutboxRow pins the wiring choice.
//
// Nil is a legitimate wiring and the consequence is deliberate: with no bus there
// is no relay either, so the row would be a promise nothing keeps.
func TestAModuleWithNoBusWritesNoOutboxRow(t *testing.T) {
	t.Parallel()

	setup := newSetupWithoutBus(t)
	ful := createTestFulfillment(t, setup)

	require.NoError(t, setup.svc.CancelFulfillment(t.Context(), ful.ID))

	assert.Empty(t, setup.store.outboxRows())

	got, err := setup.svc.GetFulfillment(t.Context(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusCanceled, got.Status,
		"the parcel is still canceled; only the announcement is absent")
}

// testLineID is the order line the parcels in this file carry.
const testLineID = "oli_cancel_events"

// createTestFulfillment opens a parcel holding two units of one line.
func createTestFulfillment(t *testing.T, setup testSetup) models.Fulfillment {
	t.Helper()

	ful, err := setup.svc.CreateFulfillment(t.Context(), service.CreateFulfillmentInput{
		Reference:        "ord_cancel_events",
		ShippingOptionID: readyOption(t, setup),
		IdempotencyKey:   "key_cancel_events",
		Items:            []service.FulfillmentItemInput{{LineItemID: testLineID, Quantity: 2}},
	})
	require.NoError(t, err)

	return ful
}
