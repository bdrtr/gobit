package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAParcelThatComesBackIsAnnounced is ADR 0423: marking a parcel come back
// lowers what the order's parcels hold, so it writes fulfillment.returned into
// the outbox with the status and publishes it after the commit, naming the
// parcel and the order it was opened for.
func TestAParcelThatComesBackIsAnnounced(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := createTestFulfillment(t, setup)
	_, err := setup.svc.MarkShipped(t.Context(), ful.ID, "TRK-1", "")
	require.NoError(t, err)

	_, err = setup.svc.MarkReturned(t.Context(), ful.ID)
	require.NoError(t, err)

	require.Equal(t, []string{"fulfillment.returned"}, setup.events.names())
	published := setup.events.published[0]
	assert.Equal(t, "fulfillment.returned:"+ful.ID, published.ID,
		"the id is derived from the parcel, so the outbox's ON CONFLICT writes one row")
	assert.Equal(t, ful.ID, published.Data["fulfillment_id"])
	assert.Equal(t, ful.Reference, published.Data["reference"],
		"the reference is what a parcel whose link was not written is recounted by")
	assert.Equal(t, "", published.Data["return_id"], "a parcel that went out brings no return back")
	assert.NotEmpty(t, published.Data["returned_at"])

	rows := setup.store.outboxRows()
	require.Len(t, rows, 1,
		"the fake store refuses an outbox write outside a transaction, so a row here "+
			"proves it commits with the status")
	assert.Equal(t, "fulfillment.returned", rows[0].name)
	assert.Equal(t, ful.Reference, rows[0].data["reference"])
}

// TestAParcelReportedComeBackTwiceIsAnnouncedOnce: the second report finds the
// parcel already returned, moves nothing and announces nothing.
func TestAParcelReportedComeBackTwiceIsAnnouncedOnce(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	ful := createTestFulfillment(t, setup)
	_, err := setup.svc.MarkShipped(t.Context(), ful.ID, "TRK-1", "")
	require.NoError(t, err)

	_, err = setup.svc.MarkReturned(t.Context(), ful.ID)
	require.NoError(t, err)
	_, err = setup.svc.MarkReturned(t.Context(), ful.ID)
	require.NoError(t, err)

	assert.Equal(t, []string{"fulfillment.returned"}, setup.events.names())
	assert.Len(t, setup.store.outboxRows(), 1)
}

// TestAReturnParcelThatComesBackNamesItsReturn: a parcel bringing a return back
// holds none of the order's outgoing units, and its come-back event says so, as
// its cancel event does (ADR 0420).
func TestAReturnParcelThatComesBackNamesItsReturn(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := returnOption(t, setup)
	setup.bound.returnLines = map[string]int64{"oli_1": 1}
	ful, err := setup.svc.CreateFulfillment(t.Context(), returnParcel(optionID, "key-coming-back", 1))
	require.NoError(t, err)
	_, err = setup.svc.MarkShipped(t.Context(), ful.ID, "TRK-R", "")
	require.NoError(t, err)

	_, err = setup.svc.MarkReturned(t.Context(), ful.ID)
	require.NoError(t, err)

	require.Len(t, setup.events.published, 1)
	assert.Equal(t, "ret_A", setup.events.published[0].Data["return_id"])
	rows := setup.store.outboxRows()
	require.Len(t, rows, 1)
	assert.Equal(t, "ret_A", rows[0].data["return_id"])
}
