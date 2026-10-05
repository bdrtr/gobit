package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// This file holds ADR 0399's decisions against the fake store: units a supplier
// owes a warehouse are an expected receipt, received through the ledger as a
// supplier_receipt naming it, and a status leaves expected once.

// receiptLater is a moment the units are expected, days ahead.
func receiptLater(days int) time.Time {
	return time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour).Truncate(time.Second)
}

// recordReceipt records an expected receipt of quantity at the location.
func recordReceipt(t *testing.T, svc *service.Service, locationID string, quantity int64) models.SupplierReceipt {
	t.Helper()

	receipt, err := svc.RecordSupplierReceipt(context.Background(), service.RecordSupplierReceiptInput{
		InventoryItemID: itemID, LocationID: locationID, Quantity: quantity,
		ExpectedAt: receiptLater(3), Reference: " PO-1 ",
	})
	require.NoError(t, err)

	return receipt
}

// TestAReceiptIsTheCountedUnitsThroughTheLedger is U1: five expected, three
// counted. The stock rises by three, one movement names the receipt, and the
// receipt is received with the movement's count and moment.
func TestAReceiptIsTheCountedUnitsThroughTheLedger(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 4, 1)
	receipt := recordReceipt(t, svc, locA, 5)
	assert.Equal(t, "PO-1", receipt.Reference, "the reference is trimmed")
	assert.Equal(t, models.SupplierReceiptExpected, receipt.Status)

	got, level, err := svc.ReceiveSupplierReceipt(ctx, itemID, receipt.ID, 3)

	require.NoError(t, err)
	require.NotNil(t, level)
	assert.Equal(t, int64(7), level.StockedQuantity, "four on the shelf and three counted")
	assert.Equal(t, int64(1), level.ReservedQuantity, "a receipt promises nothing")
	assert.Equal(t, int64(7), store.level(itemID, locA).StockedQuantity)

	moved := store.supplierMovements(itemID)
	require.Len(t, moved, 1)
	assert.Equal(t, models.MovementSupplierReceipt, moved[0].Reason)
	assert.Equal(t, int64(3), moved[0].Delta, "the count, not the quantity expected")
	assert.Equal(t, receipt.ID, moved[0].Reference)
	assert.Equal(t, int64(7), moved[0].StockedAfter)

	assert.Equal(t, models.SupplierReceiptReceived, got.Status)
	assert.Equal(t, int64(3), got.ReceivedQuantity)
	require.NotNil(t, got.ReceivedAt)
	assert.True(t, moved[0].CreatedAt.Equal(*got.ReceivedAt), "the receipt's moment is the movement's")
	assert.Equal(t, models.SupplierReceiptReceived, store.receipt(receipt.ID).Status)
}

// TestAReceiptOpensTheLevelAtANewWarehouse is U2: a warehouse that never held
// the item gets a level holding the count, explained by the receipt's movement.
func TestAReceiptOpensTheLevelAtANewWarehouse(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLocation(locB)
	receipt := recordReceipt(t, svc, locB, 4)
	before := len(store.lockOrder())

	_, level, err := svc.ReceiveSupplierReceipt(ctx, itemID, receipt.ID, 4)

	require.NoError(t, err)
	require.NotNil(t, level)
	assert.Equal(t, int64(4), level.StockedQuantity)
	assert.Equal(t, int64(4), store.level(itemID, locB).StockedQuantity)
	moved := store.supplierMovements(itemID)
	require.Len(t, moved, 1, "the level was opened by the receipt, not by a count")
	assert.Equal(t, receipt.ID, moved[0].Reference)
	assert.Equal(t, []string{"location", "item", "level", "receipt"}, store.lockOrder()[before:before+4],
		"the receipt is locked after the level, as the Store's lock order says")
}

// TestAReceiptRetriedFinishes is U3: a status leaves expected once, so a repeat
// that matches what happened answers it unchanged and any other is a conflict —
// and a receive retried after the warehouse closed still finishes.
func TestAReceiptRetriedFinishes(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 0, 0)
	received := recordReceipt(t, svc, locA, 5)
	canceled := recordReceipt(t, svc, locA, 2)

	_, _, err := svc.ReceiveSupplierReceipt(ctx, itemID, received.ID, 3)
	require.NoError(t, err)

	again, level, err := svc.ReceiveSupplierReceipt(ctx, itemID, received.ID, 3)
	require.NoError(t, err, "a client whose answer was lost asks again and is answered")
	assert.Equal(t, models.SupplierReceiptReceived, again.Status)
	require.NotNil(t, level)
	assert.Equal(t, int64(3), level.StockedQuantity)
	assert.Len(t, store.supplierMovements(itemID), 1, "the repeat wrote nothing")

	_, _, err = svc.ReceiveSupplierReceipt(ctx, itemID, received.ID, 4)
	require.Error(t, err)
	assert.Equal(t, service.CodeSupplierReceiptNotExpected, errors.CodeOf(err), "another count is not a repeat")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	_, err = svc.CancelSupplierReceipt(ctx, itemID, received.ID)
	assert.Equal(t, service.CodeSupplierReceiptNotExpected, errors.CodeOf(err), "a received receipt is not canceled")

	first, err := svc.CancelSupplierReceipt(ctx, itemID, canceled.ID)
	require.NoError(t, err)
	assert.Equal(t, models.SupplierReceiptCanceled, first.Status)
	require.NotNil(t, first.CanceledAt)
	second, err := svc.CancelSupplierReceipt(ctx, itemID, canceled.ID)
	require.NoError(t, err)
	assert.Equal(t, *first.CanceledAt, *second.CanceledAt, "a repeat does not move the moment")
	_, _, err = svc.ReceiveSupplierReceipt(ctx, itemID, canceled.ID, 2)
	assert.Equal(t, service.CodeSupplierReceiptNotExpected, errors.CodeOf(err), "a canceled receipt brings nothing")
	assert.Len(t, store.supplierMovements(itemID), 1)

	_, err = svc.AdjustInventory(ctx, itemID, locA, -3)
	require.NoError(t, err)
	_, err = svc.CloseStockLocation(ctx, locA)
	require.NoError(t, err, "nothing is expected there any more")
	_, _, err = svc.ReceiveSupplierReceipt(ctx, itemID, received.ID, 3)
	assert.NoError(t, err, "a retry finishes after the warehouse closed")
}

// TestAnotherItemsReceiptIsNotFound is U4: the path names the item, and a
// receipt of another item is answered as missing, with nothing written.
func TestAnotherItemsReceiptIsNotFound(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedItem("invitem_OTHER", "SKU-2")
	store.seedLevel(itemID, locA, 0, 0)
	receipt := recordReceipt(t, svc, locA, 5)

	_, _, err := svc.ReceiveSupplierReceipt(ctx, "invitem_OTHER", receipt.ID, 5)
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "%v", err)
	_, err = svc.CancelSupplierReceipt(ctx, "invitem_OTHER", receipt.ID)
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "%v", err)

	assert.Empty(t, store.supplierMovements(itemID))
	assert.Empty(t, store.supplierMovements("invitem_OTHER"))
	assert.Equal(t, models.SupplierReceiptExpected, store.receipt(receipt.ID).Status)
}

// TestAnExpectedReceiptKeepsItsWarehouseOpen is U5: a warehouse a supplier still
// owes units to does not close, and closes once the receipt is canceled.
func TestAnExpectedReceiptKeepsItsWarehouseOpen(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLocation(locA)
	receipt := recordReceipt(t, svc, locA, 5)

	_, err := svc.CloseStockLocation(ctx, locA)
	require.Error(t, err)
	assert.Equal(t, service.CodeLocationNotEmpty, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "1 supplier receipts are still expected")

	_, err = svc.CancelSupplierReceipt(ctx, itemID, receipt.ID)
	require.NoError(t, err)
	_, err = svc.CloseStockLocation(ctx, locA)
	assert.NoError(t, err)
}

// TestAnItemExpectingUnitsIsNotDeleted is U6.
func TestAnItemExpectingUnitsIsNotDeleted(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLocation(locA)
	receipt := recordReceipt(t, svc, locA, 5)

	err := svc.DeleteInventoryItem(ctx, itemID)
	require.Error(t, err)
	assert.Equal(t, service.CodeItemExpectsUnits, errors.CodeOf(err))
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	_, err = svc.CancelSupplierReceipt(ctx, itemID, receipt.ID)
	require.NoError(t, err)
	assert.NoError(t, svc.DeleteInventoryItem(ctx, itemID))
}

// TestADeletedItemIsOwedNothing: the foreign key takes a soft-deleted item, so
// the item's lock is what refuses a receipt for one.
func TestADeletedItemIsOwedNothing(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLocation(locA)
	require.NoError(t, svc.DeleteInventoryItem(ctx, itemID))

	_, err := svc.RecordSupplierReceipt(ctx, service.RecordSupplierReceiptInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 2, ExpectedAt: receiptLater(1),
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "%v", err)
	assert.Empty(t, store.receipts, "nothing is owed to a deleted item")
}

// TestAReceiptIsRefusedWhatItCannotBe is U7: the refusals, each before anything
// is written.
func TestAReceiptIsRefusedWhatItCannotBe(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLocation(locA)
	store.seedClosedLocation(locB)

	valid := service.RecordSupplierReceiptInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 2, ExpectedAt: receiptLater(1),
	}
	cases := map[string]struct {
		change func(*service.RecordSupplierReceiptInput)
		kind   errors.Kind
		code   string
	}{
		"a closed warehouse": {func(in *service.RecordSupplierReceiptInput) { in.LocationID = locB },
			errors.KindConflict, service.CodeLocationClosed},
		"an unknown item": {func(in *service.RecordSupplierReceiptInput) { in.InventoryItemID = unknown },
			errors.KindNotFound, ""},
		"nothing expected": {func(in *service.RecordSupplierReceiptInput) { in.Quantity = 0 },
			errors.KindInvalid, service.CodeInvalidInput},
		"less than nothing": {func(in *service.RecordSupplierReceiptInput) { in.Quantity = -1 },
			errors.KindInvalid, service.CodeInvalidInput},
		"no moment": {func(in *service.RecordSupplierReceiptInput) { in.ExpectedAt = time.Time{} },
			errors.KindInvalid, service.CodeInvalidInput},
		"a blank reference": {func(in *service.RecordSupplierReceiptInput) { in.Reference = "   " },
			errors.KindInvalid, service.CodeInvalidInput},
		"an overlong reference": {func(in *service.RecordSupplierReceiptInput) { in.Reference = strings.Repeat("r", 513) },
			errors.KindInvalid, service.CodeInvalidInput},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := valid
			tc.change(&in)

			_, err := svc.RecordSupplierReceipt(ctx, in)

			require.Error(t, err)
			assert.Equal(t, tc.kind, errors.KindOf(err), "%v", err)
			if tc.code != "" {
				assert.Equal(t, tc.code, errors.CodeOf(err))
			}
		})
	}
	listed, total, err := svc.ListSupplierReceipts(ctx, service.ListSupplierReceiptsInput{InventoryItemID: itemID})
	require.NoError(t, err)
	assert.Empty(t, listed)
	assert.Zero(t, total, "no refused receipt was written")

	receipt, err := svc.RecordSupplierReceipt(ctx, valid)
	require.NoError(t, err)
	for _, quantity := range []int64{0, -1} {
		_, _, err = svc.ReceiveSupplierReceipt(ctx, itemID, receipt.ID, quantity)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "a receive counts something: %d", quantity)
	}
	assert.Equal(t, models.SupplierReceiptExpected, store.receipt(receipt.ID).Status)
}

// TestTheReceiptsAreListedByWhenTheyAreExpected pins the listing: expected
// order, the status filter, 404 for an unknown item and 422 for an unknown
// status.
func TestTheReceiptsAreListedByWhenTheyAreExpected(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedReceipt("invsup_c", itemID, locA, 1, receiptLater(3))
	store.seedReceipt("invsup_a", itemID, locA, 1, receiptLater(1))
	store.seedReceipt("invsup_b", itemID, locA, 1, receiptLater(2))
	_, err := svc.CancelSupplierReceipt(ctx, itemID, "invsup_b")
	require.NoError(t, err)

	all, total, err := svc.ListSupplierReceipts(ctx, service.ListSupplierReceiptsInput{InventoryItemID: itemID})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, all, 3)
	assert.Equal(t, []string{"invsup_a", "invsup_b", "invsup_c"}, []string{all[0].ID, all[1].ID, all[2].ID})

	expected, total, err := svc.ListSupplierReceipts(ctx, service.ListSupplierReceiptsInput{
		InventoryItemID: itemID, Status: "expected",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, expected, 2)

	_, _, err = svc.ListSupplierReceipts(ctx, service.ListSupplierReceiptsInput{InventoryItemID: unknown})
	assert.True(t, errors.HasKind(err, errors.KindNotFound))
	_, _, err = svc.ListSupplierReceipts(ctx, service.ListSupplierReceiptsInput{InventoryItemID: itemID, Status: "late"})
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}
