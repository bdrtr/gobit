package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// TestTheForecastIsTheFillReadForward is U9: each case is a shelf, a queue of
// waiting claims and the receipts on their way, and the answer is when units
// are first left for sale at each empty warehouse once the claims take theirs,
// oldest first, whole, each where it was ranked (ADR 0392, ADR 0399).
func TestTheForecastIsTheFillReadForward(t *testing.T) {
	d1, d2 := receiptLater(1), receiptLater(2)

	type owed struct {
		quantity  int64
		locations []string
	}
	type expected struct {
		id       string
		location string
		quantity int64
		at       time.Time
	}
	cases := map[string]struct {
		shelf    map[string]int64
		claims   []owed
		receipts []expected
		want     map[string]time.Time
	}{
		"a larger claim is passed over and a later smaller one is filled": {
			claims:   []owed{{5, []string{locA}}, {2, []string{locA}}},
			receipts: []expected{{"invsup_1", locA, 3, d1}},
			want:     map[string]time.Time{locA: d1},
		},
		"a claim that takes the whole receipt moves the date to the next": {
			claims:   []owed{{3, []string{locA}}},
			receipts: []expected{{"invsup_1", locA, 3, d1}, {"invsup_2", locA, 2, d2}},
			want:     map[string]time.Time{locA: d2},
		},
		"a claim naming another warehouse is not filled here": {
			claims:   []owed{{2, []string{locB}}},
			receipts: []expected{{"invsup_1", locA, 2, d1}},
			want:     map[string]time.Time{locA: d1},
		},
		"a warehouse selling now has no date": {
			shelf:    map[string]int64{locA: 4},
			receipts: []expected{{"invsup_1", locA, 2, d1}},
			want:     map[string]time.Time{},
		},
		"receipts are taken in the order they are expected": {
			receipts: []expected{{"invsup_1", locA, 1, d2}, {"invsup_2", locA, 1, d1}},
			want:     map[string]time.Time{locA: d1},
		},
		"receipts expected together are taken by id": {
			claims:   []owed{{2, []string{locA, locB}}},
			receipts: []expected{{"invsup_1", locA, 2, d1}, {"invsup_2", locB, 2, d1}},
			want:     map[string]time.Time{locB: d1},
		},
		"claims are filled in queue order": {
			claims:   []owed{{2, []string{locA}}, {1, []string{locA}}},
			receipts: []expected{{"invsup_1", locA, 2, d1}, {"invsup_2", locA, 1, d2}},
			want:     map[string]time.Time{},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc, store := newService(t)
			store.seedItem(itemID, "SKU-1")
			store.seedLocation(locA)
			store.seedLocation(locB)
			for location, quantity := range tc.shelf {
				store.seedLevel(itemID, location, quantity, 0)
			}
			for i, c := range tc.claims {
				_, err := svc.ClaimBackorder(context.Background(), service.ClaimBackorderInput{
					InventoryItemID: itemID, OrderID: "order_1", OrderLineItemID: "oli_" + string(rune('a'+i)),
					Quantity: c.quantity, LocationIDs: c.locations,
				})
				require.NoError(t, err)
			}
			for _, r := range tc.receipts {
				store.seedReceipt(r.id, itemID, r.location, r.quantity, r.at)
			}

			got, err := svc.RestockForecast(context.Background(), []string{itemID})

			require.NoError(t, err)
			if len(tc.want) == 0 {
				assert.NotContains(t, got, itemID, "an item no receipt restocks has no key")
				return
			}
			assert.Equal(t, tc.want, got[itemID])
		})
	}
}

// TestTheForecastLeavesOutWhatIsNotExpected: a received, a canceled and an
// overdue receipt restock nothing, and an item expecting nothing has no key.
func TestTheForecastLeavesOutWhatIsNotExpected(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedItem("invitem_NONE", "SKU-2")
	store.seedLevel(itemID, locA, 0, 0)
	store.seedReceipt("invsup_late", itemID, locA, 2, time.Now().UTC().Add(-time.Hour))
	store.seedReceipt("invsup_off", itemID, locA, 2, receiptLater(1))
	_, err := svc.CancelSupplierReceipt(ctx, itemID, "invsup_off")
	require.NoError(t, err)
	store.seedReceipt("invsup_in", itemID, locA, 2, receiptLater(2))
	store.markReceived("invsup_in")

	got, err := svc.RestockForecast(ctx, []string{itemID, "invitem_NONE"})

	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestThePerWarehouseFieldsAreNotInTheDefaultSet is U10 and D258: a caller
// naming no fields receives neither per-warehouse field, and neither is
// computed; one naming the forecast gets it from one read for the whole batch.
func TestThePerWarehouseFieldsAreNotInTheDefaultSet(t *testing.T) {
	provider, store := newTestProvider(t)
	store.seedItem(itemID, "SKU-1")
	store.seedItem("invitem_TWO", "SKU-2")
	store.seedLevel(itemID, locA, 3, 0)
	store.seedLocation(locB)
	at := receiptLater(2)
	store.seedReceipt("invsup_1", itemID, locB, 4, at)
	store.seedReceipt("invsup_2", "invitem_TWO", locB, 1, at)

	records, err := provider.FetchByIDs(context.Background(), []string{itemID, "invitem_TWO"}, nil)

	require.NoError(t, err)
	require.Len(t, records, 2)
	for _, record := range records {
		assert.NotContains(t, record, service.FieldAvailableByLocation, "the default set is what a shopper may see")
		assert.NotContains(t, record, service.FieldRestockByLocation)
		assert.Contains(t, record, service.FieldAvailableQuantity)
	}
	assert.Zero(t, store.byLocationReads, "a breakdown nobody named is not computed")
	assert.Zero(t, store.forecastReads, "a forecast nobody named is not computed")

	named, err := provider.FetchByIDs(context.Background(), []string{itemID, "invitem_TWO"},
		[]string{query.IDField, service.FieldRestockByLocation})

	require.NoError(t, err)
	assert.Equal(t, 1, store.forecastReads, "one forecast for the batch, not one per record")
	byID := map[string]query.Record{}
	for _, record := range named {
		id, ok := record[query.IDField].(string)
		require.True(t, ok)
		byID[id] = record
	}
	assert.Equal(t, map[string]time.Time{locB: at}, byID[itemID][service.FieldRestockByLocation])
	assert.Equal(t, map[string]time.Time{locB: at}, byID["invitem_TWO"][service.FieldRestockByLocation])
	assert.Equal(t, models.SupplierReceiptExpected, store.receipt("invsup_1").Status)
}
