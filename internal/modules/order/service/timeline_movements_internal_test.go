package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// The timeline tells every movement it has a row for (ADR 0170).

// movementAtHour is a distinct instant per hour, so a mixed-up moment shows.
func movementAtHour(hour int) time.Time {
	return time.Date(2026, 9, 20, hour, 0, 0, 0, time.UTC)
}

// collectionWith is a payment collection record as the payment module's
// provider answers it.
func collectionWith(movements any) query.Record {
	return query.Record{
		query.IDField:         "paycol_1",
		fieldPaymentCurrency:  "TRY",
		fieldPaymentMovements: movements,
	}
}

// TestEveryMovementIsItsOwnEntry is D126: two partial refunds used to read as
// one refund of their sum, dated at the second.
func TestEveryMovementIsItsOwnEntry(t *testing.T) {
	entries, err := moneyEntries(collectionWith([]map[string]any{
		{movementID: "pay_1", movementKind: movementCapture, movementAmount: int64(10_000), movementAt: movementAtHour(9)},
		{movementID: "ref_1", movementKind: movementRefund, movementAmount: int64(1_000), movementAt: movementAtHour(10)},
		{movementID: "ref_2", movementKind: movementRefund, movementAmount: int64(500), movementAt: movementAtHour(11)},
	}))
	require.NoError(t, err)

	require.Len(t, entries, 3)
	want := []struct {
		kind, ref, clock string
		amount           int64
		hour             int
	}{
		{KindPaymentCaptured, "pay_1", ClockApplication, 10_000, 9},
		{KindPaymentRefunded, "ref_1", ClockDatabase, 1_000, 10},
		{KindPaymentRefunded, "ref_2", ClockDatabase, 500, 11},
	}
	for i, w := range want {
		assert.Equal(t, w.kind, entries[i].Kind)
		assert.Equal(t, w.ref, entries[i].RefID, "a movement names its own record")
		assert.Equal(t, w.clock, entries[i].Clock)
		assert.Equal(t, w.amount, entries[i].Amount, "an amount is what moved then, not a total")
		assert.Equal(t, "TRY", entries[i].Currency)
		require.NotNil(t, entries[i].At)
		assert.True(t, entries[i].At.Equal(movementAtHour(w.hour)))
	}
}

// TestACollectionNoMoneyMovedOnGivesNoMoneyEntry keeps "nothing moved" apart
// from a failure.
func TestACollectionNoMoneyMovedOnGivesNoMoneyEntry(t *testing.T) {
	entries, err := moneyEntries(collectionWith([]map[string]any{}))

	require.NoError(t, err)
	assert.Empty(t, entries)
}

// TestAMovementTheTimelineCannotReadIsAnError is the timeline's own rule: a
// history shorter than the truth hides a gap, so an unreadable movement fails
// the read instead of dropping out of it.
func TestAMovementTheTimelineCannotReadIsAnError(t *testing.T) {
	good := map[string]any{
		movementID: "pay_1", movementKind: movementCapture,
		movementAmount: int64(100), movementAt: movementAtHour(9),
	}
	without := func(key string) map[string]any {
		out := map[string]any{}
		for k, v := range good {
			if k != key {
				out[k] = v
			}
		}
		return out
	}
	with := func(key string, value any) map[string]any {
		out := without(key)
		out[key] = value
		return out
	}

	for name, movements := range map[string]any{
		"the field is missing":     nil,
		"the field is not a list":  "capture",
		"a movement has no id":     []map[string]any{without(movementID)},
		"a movement has no moment": []map[string]any{without(movementAt)},
		"a movement moved nothing": []map[string]any{with(movementAmount, int64(0))},
		"a movement of no kind":    []map[string]any{with(movementKind, "chargeback")},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := moneyEntries(collectionWith(movements))

			require.Error(t, err)
			assert.Equal(t, CodeCatalogReadFailed, errors.CodeOf(err))
		})
	}
}

// TestTheOrdersOwnFactsAreOnTheTimeline are the dated rows the timeline did not
// show: a line cancellation, a credit and a replacement's three moments.
func TestTheOrdersOwnFactsAreOnTheTimeline(t *testing.T) {
	dispatched, canceled := movementAtHour(14), movementAtHour(15)

	entries := factEntries("TRY",
		[]models.OrderLineCancellation{{
			ID: "olc_1", OrderLineItemID: "oli_1", Quantity: 2, CreatedAt: movementAtHour(10),
		}},
		[]models.OrderCreditLine{{ID: "ocl_1", Amount: 700, CreatedAt: movementAtHour(11)}},
		[]models.Replacement{
			{ID: "orep_1", CreatedAt: movementAtHour(12), DispatchedAt: &dispatched},
			{ID: "orep_2", CreatedAt: movementAtHour(13), CanceledAt: &canceled},
		},
	)

	cancellation := findEntry(t, entries, KindOrderLineCanceled)
	assert.Equal(t, "olc_1", cancellation.RefID)
	assert.Equal(t, "oli_1", cancellation.Detail, "the entry names the line")
	assert.Equal(t, int64(2), cancellation.Quantity)
	assert.Zero(t, cancellation.Amount, "a line cancellation moves goods, not money")

	credit := findEntry(t, entries, KindOrderCredited)
	assert.Equal(t, int64(700), credit.Amount)
	assert.Equal(t, "TRY", credit.Currency)

	assert.Len(t, filterKind(entries, KindReplacementOpened), 2)
	assert.Equal(t, "orep_1", findEntry(t, entries, KindReplacementDispatched).RefID)
	assert.Equal(t, "orep_2", findEntry(t, entries, KindReplacementCanceled).RefID)

	for i := range entries {
		assert.Equal(t, ClockDatabase, entries[i].Clock, "%s", entries[i].Kind)
		assert.NotNil(t, entries[i].At, "%s", entries[i].Kind)
	}
}

// TestAFundedExchangeReportsItsFunding is the money an exchange took, on its
// own collection.
func TestAFundedExchangeReportsItsFunding(t *testing.T) {
	funded := movementAtHour(12)

	entries := exchangeEntries([]models.Exchange{{
		ID: "oexc_1", Status: models.ExchangeFunded, DifferenceDue: 2_500,
		PaymentCollectionID: "paycol_2", CreatedAt: movementAtHour(10), FundedAt: &funded,
	}})

	funding := findEntry(t, entries, KindExchangeFunded)
	assert.Equal(t, "oexc_1", funding.RefID)
	assert.Equal(t, int64(2_500), funding.Amount)
	assert.Equal(t, "paycol_2", funding.Detail, "the entry names the collection the money is on")
	assert.True(t, funding.At.Equal(funded))
}

// TestTheErasureIsDated is what a reading of the order at an earlier moment
// needs: the contact it shows now is not the contact then.
func TestTheErasureIsDated(t *testing.T) {
	order := archivedOrder()
	erased := order.ArchivedAt.Add(time.Hour)
	order.PersonalDataErasedAt = &erased

	entry := findEntry(t, orderEntries(order), KindOrderDataErased)

	assert.True(t, entry.At.Equal(erased))
	assert.Equal(t, ClockDatabase, entry.Clock)
}

// TestTheCustomerSeesTheNewGoodsAndNotTheNewMoney classifies ADR 0170's kinds:
// the goods cross, the money and the shop's own records do not.
func TestTheCustomerSeesTheNewGoodsAndNotTheNewMoney(t *testing.T) {
	kept := customerVisible([]TimelineEntry{
		{Kind: KindOrderLineCanceled},
		{Kind: KindReplacementOpened},
		{Kind: KindOrderCredited, Amount: 700},
		{Kind: KindExchangeFunded, Amount: 2_500},
		{Kind: KindOrderDataErased},
	})

	assert.Equal(t, []string{KindOrderLineCanceled, KindReplacementOpened}, kindsOf(kept))
}

// filterKind returns the entries of one kind.
func filterKind(entries []TimelineEntry, kind string) []TimelineEntry {
	var out []TimelineEntry
	for i := range entries {
		if entries[i].Kind == kind {
			out = append(out, entries[i])
		}
	}

	return out
}
