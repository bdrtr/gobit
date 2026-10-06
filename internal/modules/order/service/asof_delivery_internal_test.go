package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// Where an order was going, and on which service, at a moment (ADR 0411).

// shippingRow is a shipping address row written at a moment.
func shippingRow(id string, created time.Time, superseded *time.Time) models.OrderAddress {
	return models.OrderAddress{
		ID: id, Type: models.AddressShipping, Address1: id + " Road",
		CreatedAt: created, SupersededAt: superseded,
	}
}

// rowAt names the shipping row the reading chose at a moment.
func rowAt(t *testing.T, at time.Time, rows []models.OrderAddress) string {
	t.Helper()

	row := shippingAddressAt(at, rows, models.ContactHeld)
	require.NotNil(t, row, "a row is in force at %s", at)

	return row.ID
}

// TestTheAddressIsTheOneWrittenLastByTheMoment walks the corrections of one
// order: each moment reads the row written last at or before it, including the
// two moments the closing stamp alone cannot answer.
func TestTheAddressIsTheOneWrittenLastByTheMoment(t *testing.T) {
	gapClosed := hour(4)
	overlapClosed := hour(6).Add(time.Minute)
	rows := []models.OrderAddress{
		{ID: "billing", Type: models.AddressBilling, CreatedAt: hour(1), Address1: "Billing Road"},
		shippingRow("placed", hour(1), ptrAt(2)),
		shippingRow("corrected", hour(2), &gapClosed),
		// Since migration 000037 the successor is stamped after its
		// predecessor closed, on the same clock: a moment between the two is in
		// neither row's open interval.
		shippingRow("after_gap", hour(4).Add(time.Microsecond), &overlapClosed),
		// Before 000037 the successor took its transaction's start, which can
		// be before its predecessor closed: a moment between the two is in both.
		shippingRow("overlapping", hour(6), nil),
	}

	assert.Equal(t, "placed", rowAt(t, hour(1), rows))
	assert.Equal(t, "placed", rowAt(t, hour(1).Add(30*time.Minute), rows))
	assert.Equal(t, "corrected", rowAt(t, hour(3), rows))
	assert.Equal(t, "corrected", rowAt(t, gapClosed, rows),
		"between the closing and its successor the closed row was the last written")
	assert.Equal(t, "after_gap", rowAt(t, hour(5), rows))
	assert.Equal(t, "overlapping", rowAt(t, hour(6).Add(30*time.Second), rows),
		"of two rows open at once, the one written last")
	assert.Equal(t, "overlapping", rowAt(t, endOfTime, rows), "and it is the current row")
}

// TestTheAddressPlacedWithStandsBeforeItsOwnStamp reads a moment after the
// order but before every shipping row's stamp: the placing transaction wrote
// them later than placed_at, and the order was going to the first of them. The
// billing row, stamped earlier, is never the shipping address.
func TestTheAddressPlacedWithStandsBeforeItsOwnStamp(t *testing.T) {
	placed := hour(1)
	rows := []models.OrderAddress{
		{ID: "billing", Type: models.AddressBilling, CreatedAt: placed.Add(-time.Millisecond)},
		shippingRow("first", placed.Add(time.Millisecond), ptrAt(3)),
		shippingRow("second", hour(3), nil),
	}

	assert.Equal(t, "first", rowAt(t, placed, rows))
	assert.Nil(t, shippingAddressAt(placed, rows[:1], models.ContactHeld),
		"an order that recorded no shipping address names none")
}

// TestAnErasedAddressIsNamedWithoutItsContent keeps the row's name and moment
// after an erasure and drops its content, which the erasure emptied.
func TestAnErasedAddressIsNamedWithoutItsContent(t *testing.T) {
	rows := []models.OrderAddress{shippingRow("placed", hour(1), nil)}

	held := shippingAddressAt(hour(2), rows, models.ContactHeld)
	require.NotNil(t, held)
	require.NotNil(t, held.Address)
	assert.Equal(t, "placed Road", held.Address.Address1)

	for _, contact := range []models.ContactAsOf{models.ContactErased, models.ContactErasedSince} {
		row := shippingAddressAt(hour(2), rows, contact)
		require.NotNil(t, row, "%s", contact)
		assert.Equal(t, models.AddressAsOf{ID: "placed", Since: hour(1)}, *row, "%s", contact)
	}
}

// deliveryFixture is two methods sold at hour 1 and three changes: the first
// method put on a cheaper service at 3 and a dearer one at 5, the second on a
// dearer service at 4.
func deliveryFixture() (placed time.Time, methods []models.OrderShippingMethod, changes []models.DeliveryChange) {
	placed = hour(1)
	created := placed.Add(time.Second)
	methods = []models.OrderShippingMethod{
		{ID: "oship_1", ShippingOptionID: "sopt_standard", Name: "Standard", Amount: 1500, CreatedAt: created},
		{ID: "oship_2", ShippingOptionID: "sopt_bulky", Name: "Bulky", Amount: 900, CreatedAt: created},
	}
	changes = []models.DeliveryChange{
		{
			ID: "odchg_1", ShippingMethodID: "oship_1", ShippingOptionID: "sopt_pickup", Name: "Pickup",
			Amount: 1000, Difference: -500, CreditLineID: "ocl_1", CreatedAt: hour(3),
		},
		{
			ID: "odchg_2", ShippingMethodID: "oship_2", ShippingOptionID: "sopt_freight", Name: "Freight",
			Amount: 1200, Difference: 300, PaymentCollectionID: "paycol_2", CreatedAt: hour(4),
		},
		{
			ID: "odchg_3", ShippingMethodID: "oship_1", ShippingOptionID: "sopt_express", Name: "Express",
			Amount: 1100, Difference: 100, PaymentCollectionID: "paycol_3", CreatedAt: hour(5),
		},
	}

	return placed, methods, changes
}

// TestADeliveryIsTheMethodWithItsLatestChangeByTheMoment reads each method as
// sold until its first change, then as its latest change made by the moment,
// and a change made later or to the other method does not reach it.
func TestADeliveryIsTheMethodWithItsLatestChangeByTheMoment(t *testing.T) {
	placed, methods, changes := deliveryFixture()
	soldFirst := models.DeliveryAsOf{
		ShippingMethodID: "oship_1", ShippingOptionID: "sopt_standard", Name: "Standard", Amount: 1500, Since: placed,
	}
	soldSecond := models.DeliveryAsOf{
		ShippingMethodID: "oship_2", ShippingOptionID: "sopt_bulky", Name: "Bulky", Amount: 900, Since: placed,
	}
	cheaper := models.DeliveryAsOf{
		ShippingMethodID: "oship_1", ShippingOptionID: "sopt_pickup", Name: "Pickup", Amount: 1000,
		ChangeID: "odchg_1", Difference: -500, CreditLineID: "ocl_1", Since: hour(3),
	}
	freight := models.DeliveryAsOf{
		ShippingMethodID: "oship_2", ShippingOptionID: "sopt_freight", Name: "Freight", Amount: 1200,
		ChangeID: "odchg_2", Difference: 300, PaymentCollectionID: "paycol_2", Since: hour(4),
	}
	express := models.DeliveryAsOf{
		ShippingMethodID: "oship_1", ShippingOptionID: "sopt_express", Name: "Express", Amount: 1100,
		ChangeID: "odchg_3", Difference: 100, PaymentCollectionID: "paycol_3", Since: hour(5),
	}

	for _, tc := range []struct {
		at   time.Time
		want []models.DeliveryAsOf
	}{
		{at: placed, want: []models.DeliveryAsOf{soldFirst, soldSecond}},
		{at: hour(2), want: []models.DeliveryAsOf{soldFirst, soldSecond}},
		{at: hour(3), want: []models.DeliveryAsOf{cheaper, soldSecond}},
		{at: hour(4), want: []models.DeliveryAsOf{cheaper, freight}},
		{at: hour(5), want: []models.DeliveryAsOf{express, freight}},
	} {
		assert.Equal(t, tc.want, deliveriesAt(tc.at, placed, methods, changes), "at %s", tc.at)
	}

	assert.Empty(t, deliveriesAt(hour(5), placed, nil, nil), "an order sold no delivery has none")
	assert.NotNil(t, deliveriesAt(hour(5), placed, nil, nil), "and says so with an empty list")
}

// TestTheDeliveriesReadAfterEveryChangeAreTheCurrentOnes binds the reading to
// the order's own fold: read after every change, each delivery is the one
// [models.CurrentDeliveries] says the order is on.
func TestTheDeliveriesReadAfterEveryChangeAreTheCurrentOnes(t *testing.T) {
	placed, methods, changes := deliveryFixture()

	for name, fixture := range map[string][]models.DeliveryChange{
		"no change":    nil,
		"one change":   changes[:1],
		"every change": changes,
	} {
		current := models.CurrentDeliveries(methods, fixture)
		read := deliveriesAt(endOfTime, placed, methods, fixture)

		require.Len(t, read, len(current), name)
		for i := range current {
			assert.Equal(t, current[i].ID, read[i].ShippingMethodID, name)
			assert.Equal(t, current[i].ShippingOptionID, read[i].ShippingOptionID, name)
			assert.Equal(t, current[i].Name, read[i].Name, name)
			assert.Equal(t, current[i].Amount, read[i].Amount, name)
		}
	}
}

// TestTheReadingCarriesWhereTheOrderWasGoing is the derivation's wiring: the
// order's rows reach the reading through orderAsOf.
func TestTheReadingCarriesWhereTheOrderWasGoing(t *testing.T) {
	placed, methods, changes := deliveryFixture()
	in := &asOfInputs{
		order: models.OrderDetail{
			Order:           models.Order{PlacedAt: placed},
			ShippingMethods: methods, DeliveryChanges: changes,
		},
		addresses: []models.OrderAddress{shippingRow("placed", placed, ptrAt(3)), shippingRow("corrected", hour(3), nil)},
	}

	out := orderAsOf(hour(2), in)

	require.NotNil(t, out.ShippingAddress)
	assert.Equal(t, "placed", out.ShippingAddress.ID)
	require.Len(t, out.Deliveries, 2)
	assert.Empty(t, out.Deliveries[0].ChangeID)
}
