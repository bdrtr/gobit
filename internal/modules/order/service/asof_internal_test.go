package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// An order read as it stood at a moment (ADR 0171).

// hour is a distinct instant per hour of one day.
func hour(h int) time.Time { return time.Date(2026, 9, 21, h, 0, 0, 0, time.UTC) }

// ptrAt is a stamp.
func ptrAt(h int) *time.Time { at := hour(h); return &at }

// endOfTime is a moment after every stamp in these tests.
var endOfTime = hour(23)

// TestAStatusReadAfterEveryStampIsTheRecordedStatus is the cross-check that
// holds the derivation to the records: read after its last stamp, every record
// in every state it can reach answers the status it has recorded. A derivation
// that disagreed with the record today would disagree with it about any past.
func TestAStatusReadAfterEveryStampIsTheRecordedStatus(t *testing.T) {
	t.Run("order", func(t *testing.T) {
		for _, order := range []models.Order{
			{Status: models.OrderPending},
			{Status: models.OrderCompleted, CompletedAt: ptrAt(2)},
			{Status: models.OrderCanceled, CanceledAt: ptrAt(2)},
			{Status: models.OrderArchived, CompletedAt: ptrAt(2), ArchivedAt: ptrAt(3)},
		} {
			status := orderStatusAt(&order, endOfTime)
			require.NotNil(t, status, "%s", order.Status)
			assert.Equal(t, order.Status, *status)
		}
	})

	t.Run("after-sales", func(t *testing.T) {
		in := &asOfInputs{
			returns: []models.Return{
				{ID: "r_requested", Status: models.ReturnRequested, CreatedAt: hour(1)},
				{ID: "r_received", Status: models.ReturnReceived, CreatedAt: hour(1), ReceivedAt: ptrAt(2)},
				{ID: "r_canceled", Status: models.ReturnCanceled, CreatedAt: hour(1), CanceledAt: ptrAt(2)},
			},
			claims: []models.Claim{
				{ID: "c_requested", Status: models.ClaimRequested, CreatedAt: hour(1)},
				{ID: "c_completed", Status: models.ClaimCompleted, CreatedAt: hour(1), CompletedAt: ptrAt(2)},
				{ID: "c_canceled", Status: models.ClaimCanceled, CreatedAt: hour(1), CanceledAt: ptrAt(2)},
			},
			exchanges: []models.Exchange{
				{ID: "e_requested", Status: models.ExchangeRequested, CreatedAt: hour(1)},
				{ID: "e_funded", Status: models.ExchangeFunded, CreatedAt: hour(1), FundedAt: ptrAt(2)},
				{ID: "e_completed", Status: models.ExchangeCompleted, CreatedAt: hour(1), CompletedAt: ptrAt(2)},
				{
					ID: "e_funded_completed", Status: models.ExchangeCompleted, CreatedAt: hour(1),
					FundedAt: ptrAt(2), CompletedAt: ptrAt(3),
				},
				{ID: "e_canceled", Status: models.ExchangeCanceled, CreatedAt: hour(1), CanceledAt: ptrAt(2)},
				{
					ID: "e_funded_withdrawn", Status: models.ExchangeCanceled, CreatedAt: hour(1),
					FundedAt: ptrAt(2), CanceledAt: ptrAt(3),
				},
			},
			replacements: []models.Replacement{
				{ID: "p_requested", Status: models.ReplacementRequested, CreatedAt: hour(1)},
				{ID: "p_dispatched", Status: models.ReplacementDispatched, CreatedAt: hour(1), DispatchedAt: ptrAt(2)},
				{ID: "p_canceled", Status: models.ReplacementCanceled, CreatedAt: hour(1), CanceledAt: ptrAt(2)},
			},
		}
		recorded := map[string]string{}
		for i := range in.returns {
			recorded[in.returns[i].ID] = string(in.returns[i].Status)
		}
		for i := range in.claims {
			recorded[in.claims[i].ID] = string(in.claims[i].Status)
		}
		for i := range in.exchanges {
			recorded[in.exchanges[i].ID] = string(in.exchanges[i].Status)
		}
		for i := range in.replacements {
			recorded[in.replacements[i].ID] = string(in.replacements[i].Status)
		}

		out := orderAsOf(endOfTime, in)

		derived := map[string]string{}
		for _, list := range [][]models.RecordAsOf{out.Returns, out.Claims, out.Exchanges, out.Replacements} {
			for i := range list {
				derived[list[i].ID] = list[i].Status
			}
		}
		assert.Equal(t, recorded, derived)
	})

	t.Run("shipment", func(t *testing.T) {
		parcel := func(id, status string, stamps map[string]*time.Time) query.Record {
			record := query.Record{query.IDField: id, fieldShipmentStatus: status, fieldShipmentCreated: hour(1)}
			for field, at := range stamps {
				record[field] = at
			}
			return record
		}
		in := &asOfInputs{shipments: []query.Record{
			parcel("s_pending", shipmentPending, nil),
			parcel("s_shipped", shipmentShipped, map[string]*time.Time{fieldShipmentShipped: ptrAt(2)}),
			parcel("s_delivered", shipmentDelivered, map[string]*time.Time{
				fieldShipmentShipped: ptrAt(2), fieldShipmentDelivered: ptrAt(3),
			}),
			parcel("s_delivered_unshipped", shipmentDelivered, map[string]*time.Time{
				fieldShipmentDelivered: ptrAt(3),
			}),
			parcel("s_canceled", shipmentCanceled, map[string]*time.Time{fieldShipmentCanceled: ptrAt(2)}),
			parcel("s_returned", shipmentReturned, map[string]*time.Time{
				fieldShipmentShipped: ptrAt(2), fieldShipmentDelivered: ptrAt(3), fieldShipmentReturned: ptrAt(4),
			}),
		}}

		out := orderAsOf(endOfTime, in)

		require.Len(t, out.Shipments, len(in.shipments))
		for i := range out.Shipments {
			assert.Equal(t, recordText(in.shipments[i], fieldShipmentStatus), out.Shipments[i].Status,
				"%s", out.Shipments[i].ID)
		}
	})
}

// TestAStatusIsTheOneStandingAtTheMoment reads one order between its stamps; a
// stamp counts from its own instant on.
func TestAStatusIsTheOneStandingAtTheMoment(t *testing.T) {
	order := models.Order{Status: models.OrderArchived, CompletedAt: ptrAt(5), ArchivedAt: ptrAt(9)}

	for moment, want := range map[int]models.OrderStatus{
		4: models.OrderPending, 5: models.OrderCompleted, 8: models.OrderCompleted,
		9: models.OrderArchived, 12: models.OrderArchived,
	} {
		status := orderStatusAt(&order, hour(moment))
		require.NotNil(t, status)
		assert.Equal(t, want, *status, "at %02d:00", moment)
	}
}

// TestAnUndatedArchiveIsNotGuessed is the one moment the records cannot place:
// archived before the archiving was dated, and read after the completion.
func TestAnUndatedArchiveIsNotGuessed(t *testing.T) {
	order := models.Order{Status: models.OrderArchived, CompletedAt: ptrAt(5)}

	assert.Nil(t, orderStatusAt(&order, hour(6)), "completed or archived: the records do not say")
	before := orderStatusAt(&order, hour(4))
	require.NotNil(t, before)
	assert.Equal(t, models.OrderPending, *before, "before the completion it was pending either way")
}

// TestTheMoneyIsWhatHadMovedByTheMoment sums the movements and the credits up to
// the moment, and owes by the live order's own formula.
func TestTheMoneyIsWhatHadMovedByTheMoment(t *testing.T) {
	in := &asOfInputs{
		order: models.OrderDetail{Order: models.Order{CurrencyCode: "TRY", Total: 10_000}},
		movements: []paymentMovement{
			{ID: "pay_1", Kind: movementCapture, Amount: 10_000, At: hour(2)},
			{ID: "ref_1", Kind: movementRefund, Amount: 1_000, At: hour(4)},
			{ID: "ref_2", Kind: movementRefund, Amount: 500, At: hour(6)},
		},
		credits: []models.OrderCreditLine{{ID: "ocl_1", Amount: 300, CreatedAt: hour(5)}},
	}

	for moment, want := range map[int]models.MoneyAsOf{
		1: {Currency: "TRY", Total: 10_000, Outstanding: 10_000},
		2: {Currency: "TRY", Total: 10_000, Captured: 10_000},
		4: {Currency: "TRY", Total: 10_000, Captured: 10_000, Refunded: 1_000, Outstanding: 1_000},
		5: {Currency: "TRY", Total: 10_000, Captured: 10_000, Refunded: 1_000, Credited: 300, Outstanding: 700},
		6: {Currency: "TRY", Total: 10_000, Captured: 10_000, Refunded: 1_500, Credited: 300, Outstanding: 1_200},
	} {
		assert.Equal(t, want, moneyAt(hour(moment), in), "at %02d:00", moment)
	}
}

// TestARecordThatDidNotExistYetIsLeftOut keeps a later record off an earlier
// moment, and a line cancellation too.
func TestARecordThatDidNotExistYetIsLeftOut(t *testing.T) {
	in := &asOfInputs{
		order: models.OrderDetail{Items: []models.OrderLineItem{{ID: "oli_1", Quantity: 3}}},
		returns: []models.Return{
			{ID: "r_early", Status: models.ReturnReceived, CreatedAt: hour(2), ReceivedAt: ptrAt(8)},
			{ID: "r_late", Status: models.ReturnRequested, CreatedAt: hour(7)},
		},
		cancellations: []models.OrderLineCancellation{
			{OrderLineItemID: "oli_1", Quantity: 1, CreatedAt: hour(3)},
			{OrderLineItemID: "oli_1", Quantity: 1, CreatedAt: hour(9)},
		},
	}

	out := orderAsOf(hour(5), in)

	require.Len(t, out.Returns, 1)
	assert.Equal(t, models.RecordAsOf{ID: "r_early", Status: "requested", Since: hour(2)}, out.Returns[0])
	require.Len(t, out.Lines, 1)
	assert.Equal(t, int64(3), out.Lines[0].Quantity, "what was bought never changes")
	assert.Equal(t, int64(1), out.Lines[0].Canceled, "only the cancellation made by the moment")
}

// TestTheContactIsHeldUntilItsErasure is the one piece of the order whose past
// the records lose.
func TestTheContactIsHeldUntilItsErasure(t *testing.T) {
	held := models.Order{}
	erased := models.Order{PersonalDataErasedAt: ptrAt(10)}

	assert.Equal(t, models.ContactHeld, contactAt(&held, hour(12)))
	assert.Equal(t, models.ContactErasedSince, contactAt(&erased, hour(9)),
		"held then, gone now: the order can no longer say what it was")
	assert.Equal(t, models.ContactErased, contactAt(&erased, hour(10)))
}

// TestAClockSkewDoesNotHideATransition is the two clocks of a parcel: created on
// the database's, shipped on the application's. A shipping stamp a little
// behind the creation still decides.
func TestAClockSkewDoesNotHideATransition(t *testing.T) {
	skewed := hour(3).Add(-time.Second)
	in := &asOfInputs{shipments: []query.Record{{
		query.IDField: "ful_1", fieldShipmentCreated: hour(3), fieldShipmentShipped: &skewed,
	}}}

	out := orderAsOf(hour(4), in)

	require.Len(t, out.Shipments, 1)
	assert.Equal(t, shipmentShipped, out.Shipments[0].Status)
}
