package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// This file holds ADR 0392: a line the checkout let through without stock is a
// claim, filled whole and oldest first by the next write that makes enough units
// sellable where its order may ship from, and withdrawn by a write-off as far as
// the units will not leave.

const (
	boItem  = "invitem_bo"
	boOrder = "order_bo"
	boLocA  = "sloc_a"
	boLocB  = "sloc_b"
)

// claim records a waiting claim for the line, fillable at the locations.
func claim(t *testing.T, svc *service.Service, line string, quantity int64, locations ...string) models.Backorder {
	t.Helper()

	out, err := svc.ClaimBackorder(context.Background(), service.ClaimBackorderInput{
		InventoryItemID: boItem, OrderID: boOrder, OrderLineItemID: line,
		Quantity: quantity, LocationIDs: locations,
	})
	require.NoError(t, err)
	require.Equal(t, models.BackorderWaiting, out.Status)

	return out
}

// backorderService is a service with the item and the two locations, and no
// level yet.
func backorderService(t *testing.T) (*service.Service, *fakeStore) {
	t.Helper()

	svc, store := newService(t)
	store.seedItem(boItem, "SKU-BO")
	store.seedLocation(boLocA)
	store.seedLocation(boLocB)

	return svc, store
}

// TestAnArrivalGoesToTheOldestClaimItCompletes: four units arriving for claims
// of 2, 2 and 3 fill the first two and leave the third waiting; three units for
// claims of 5 and then 2 pass the 5 over, fill the 2, and leave one on sale.
func TestAnArrivalGoesToTheOldestClaimItCompletes(t *testing.T) {
	ctx := context.Background()

	t.Run("oldest first", func(t *testing.T) {
		svc, store := backorderService(t)
		first := claim(t, svc, "oli_1", 2, boLocA)
		second := claim(t, svc, "oli_2", 2, boLocA)
		third := claim(t, svc, "oli_3", 3, boLocA)

		level, err := svc.SetInventoryLevel(ctx, boItem, boLocA, 4)
		require.NoError(t, err)

		assert.Equal(t, models.BackorderFilled, store.backorder(first.ID).Status)
		assert.Equal(t, models.BackorderFilled, store.backorder(second.ID).Status)
		assert.Equal(t, models.BackorderWaiting, store.backorder(third.ID).Status)
		assert.Equal(t, int64(0), level.StockedQuantity)
	})

	t.Run("a claim that does not fit is passed over, whole", func(t *testing.T) {
		svc, store := backorderService(t)
		large := claim(t, svc, "oli_1", 5, boLocA)
		small := claim(t, svc, "oli_2", 2, boLocA)

		level, err := svc.SetInventoryLevel(ctx, boItem, boLocA, 3)
		require.NoError(t, err)

		assert.Equal(t, models.BackorderWaiting, store.backorder(large.ID).Status)
		assert.Empty(t, store.backorder(large.ID).ReservationID, "no part of the large claim was held")
		assert.Equal(t, models.BackorderFilled, store.backorder(small.ID).Status)
		assert.Equal(t, int64(1), level.StockedQuantity, "the unit no claim could take stays on sale")
		assert.Equal(t, int64(0), level.ReservedQuantity)
	})

	t.Run("one the earlier fills left too little for is passed over, whole", func(t *testing.T) {
		svc, store := backorderService(t)
		first := claim(t, svc, "oli_1", 3, boLocA)
		second := claim(t, svc, "oli_2", 2, boLocA)
		third := claim(t, svc, "oli_3", 1, boLocA)

		level, err := svc.SetInventoryLevel(ctx, boItem, boLocA, 4)
		require.NoError(t, err)

		assert.Equal(t, models.BackorderFilled, store.backorder(first.ID).Status)
		assert.Equal(t, models.BackorderWaiting, store.backorder(second.ID).Status,
			"one unit was left and the claim owes two")
		assert.Empty(t, store.backorder(second.ID).ReservationID, "no part of it was held")
		assert.Equal(t, models.BackorderFilled, store.backorder(third.ID).Status,
			"the last unit goes to the next claim it completes")
		assert.Equal(t, int64(0), level.StockedQuantity)
	})
}

// TestAClaimIsFilledOnlyWhereItWasRanked: units arriving at a warehouse the
// claim does not name stay on sale there.
func TestAClaimIsFilledOnlyWhereItWasRanked(t *testing.T) {
	ctx := context.Background()
	svc, store := backorderService(t)
	waiting := claim(t, svc, "oli_1", 2, boLocB)

	level, err := svc.SetInventoryLevel(ctx, boItem, boLocA, 5)
	require.NoError(t, err)
	assert.Equal(t, int64(5), level.StockedQuantity)
	assert.Equal(t, models.BackorderWaiting, store.backorder(waiting.ID).Status)

	level, err = svc.SetInventoryLevel(ctx, boItem, boLocB, 5)
	require.NoError(t, err)
	assert.Equal(t, int64(3), level.StockedQuantity)
	assert.Equal(t, boLocB, store.backorder(waiting.ID).FilledLocationID)
}

// TestEveryWriteThatRaisesTheSellableQuantityFills runs each write that makes
// units sellable against a waiting claim of two.
func TestEveryWriteThatRaisesTheSellableQuantityFills(t *testing.T) {
	ctx := context.Background()

	cases := map[string]struct {
		seed  func(store *fakeStore)
		write func(svc *service.Service) error
	}{
		"a count that opens the level": {
			write: func(svc *service.Service) error {
				_, err := svc.SetInventoryLevel(ctx, boItem, boLocA, 2)
				return err
			},
		},
		"a count over an existing level": {
			seed: func(store *fakeStore) { store.seedLevel(boItem, boLocA, 0, 0) },
			write: func(svc *service.Service) error {
				_, err := svc.SetInventoryLevel(ctx, boItem, boLocA, 2)
				return err
			},
		},
		"a count from what the form showed": {
			seed: func(store *fakeStore) { store.seedLevel(boItem, boLocA, 1, 0) },
			write: func(svc *service.Service) error {
				_, err := svc.SetInventoryLevelFrom(ctx, boItem, boLocA, 1, 2)
				return err
			},
		},
		"an adjustment": {
			seed: func(store *fakeStore) { store.seedLevel(boItem, boLocA, 0, 0) },
			write: func(svc *service.Service) error {
				_, err := svc.AdjustInventory(ctx, boItem, boLocA, 2)
				return err
			},
		},
		"a return": {
			seed: func(store *fakeStore) { store.seedLevel(boItem, boLocA, 0, 0) },
			write: func(svc *service.Service) error {
				_, err := svc.RestockInventory(ctx, boItem, boLocA, 2)
				return err
			},
		},
		"a write-off coming back": {
			seed: func(store *fakeStore) { store.seedLevel(boItem, boLocA, 0, 0) },
			write: func(svc *service.Service) error {
				_, err := svc.ReturnCanceledInventory(ctx, boItem, boLocA, "oli_other", 2, "lcan_1")
				return err
			},
		},
		"a replacement recalled": {
			seed: func(store *fakeStore) {
				store.seedLevel(boItem, boLocA, 0, 0)
				res := store.seedReservation("invres_rep", boItem, boLocA, 2, models.ReservationConfirmed)
				res.Purpose = models.PurposeReplacement
				store.mu.Lock()
				store.reservations[res.ID] = res
				store.mu.Unlock()
			},
			write: func(svc *service.Service) error {
				_, err := svc.RecallReplacementUnits(ctx, "invres_rep")
				return err
			},
		},
		"a reservation released": {
			seed: func(store *fakeStore) {
				store.seedLevel(boItem, boLocA, 2, 2)
				store.seedReservation("invres_held", boItem, boLocA, 2, models.ReservationActive)
			},
			write: func(svc *service.Service) error {
				return svc.ReleaseReservation(ctx, "invres_held")
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc, store := backorderService(t)
			if tc.seed != nil {
				tc.seed(store)
			}
			waiting := claim(t, svc, "oli_1", 2, boLocA)

			require.NoError(t, tc.write(svc))

			got := store.backorder(waiting.ID)
			assert.Equal(t, models.BackorderFilled, got.Status, "the units went to the waiting order")
			assert.Equal(t, int64(0), store.level(boItem, boLocA).Available(), "nothing was left on sale")
		})
	}
}

// TestAWriteThatRaisesNothingDoesNotReadTheQueue: a reservation lowers what is
// sellable, and a confirm and a count of what is there leave it equal, so none
// of them reads the claims, though one waits at that level that would fit.
func TestAWriteThatRaisesNothingDoesNotReadTheQueue(t *testing.T) {
	ctx := context.Background()
	svc, store := backorderService(t)
	store.seedLevel(boItem, boLocA, 10, 0)
	waiting := claim(t, svc, "oli_near", 2, boLocA)

	reserved, err := svc.Reserve(ctx, service.ReserveInput{InventoryItemID: boItem, LocationID: boLocA, Quantity: 3})
	require.NoError(t, err)
	require.NoError(t, svc.ConfirmReservation(ctx, reserved.ID, "order_other"))
	_, err = svc.SetInventoryLevel(ctx, boItem, boLocA, 7)
	require.NoError(t, err, "a count that writes what is there moves nothing")

	assert.Zero(t, store.queueReadCount(), "no write here made a unit sellable")
	assert.Equal(t, models.BackorderWaiting, store.backorder(waiting.ID).Status,
		"a claim is filled by an arrival or a settlement, not by a write that raises nothing")
}

// TestAFillIsAReservationOfTheOrderConfirmed: the fill holds the units for the
// order line and deducts them at once, so the ledger shows a sale naming the
// reservation and the order, and the claim names its reservation and shelf.
func TestAFillIsAReservationOfTheOrderConfirmed(t *testing.T) {
	ctx := context.Background()
	svc, store := backorderService(t)
	waiting := claim(t, svc, "oli_1", 2, boLocA)

	_, err := svc.SetInventoryLevel(ctx, boItem, boLocA, 5)
	require.NoError(t, err)

	filled := store.backorder(waiting.ID)
	require.NotEmpty(t, filled.ReservationID)
	assert.Equal(t, boLocA, filled.FilledLocationID)

	res := store.reservation(filled.ReservationID)
	assert.Equal(t, models.ReservationConfirmed, res.Status)
	assert.Equal(t, models.PurposeSale, res.Purpose)
	assert.Equal(t, "oli_1", res.LineItemID, "the reservation is the order line's")
	assert.Equal(t, int64(2), res.Quantity)

	movements := store.movementsFor(boItem)
	require.Len(t, movements, 2)
	assert.Equal(t, models.MovementStockCount, movements[0].Reason)
	assert.Equal(t, int64(5), movements[0].Delta)
	assert.Equal(t, models.MovementSale, movements[1].Reason)
	assert.Equal(t, int64(-2), movements[1].Delta)
	assert.Equal(t, filled.ReservationID, movements[1].ReservationID)
	assert.Equal(t, boOrder, movements[1].Reference)

	level := store.level(boItem, boLocA)
	assert.Equal(t, int64(3), level.StockedQuantity)
	assert.Equal(t, int64(0), level.ReservedQuantity, "the hold was deducted, not left standing")
}

// TestALevelWriteAnswersTheCountAfterTheFill: the answer is the level after the
// claim took its units, not the count written.
func TestALevelWriteAnswersTheCountAfterTheFill(t *testing.T) {
	ctx := context.Background()
	svc, _ := backorderService(t)
	claim(t, svc, "oli_1", 2, boLocA)

	level, err := svc.SetInventoryLevel(ctx, boItem, boLocA, 5)
	require.NoError(t, err)
	assert.Equal(t, int64(3), level.StockedQuantity)

	claim(t, svc, "oli_2", 1, boLocA)
	level, err = svc.AdjustInventory(ctx, boItem, boLocA, 4)
	require.NoError(t, err)
	assert.Equal(t, int64(6), level.StockedQuantity, "3 + 4, less the 1 the second order waited for")
}

// TestAFillTakesWhatIsStillOwed: a claim of five with two withdrawn is filled by
// three units.
func TestAFillTakesWhatIsStillOwed(t *testing.T) {
	ctx := context.Background()
	svc, store := backorderService(t)
	waiting := claim(t, svc, "oli_1", 5, boLocA)
	_, err := svc.SettleBackorder(ctx, "oli_1", 5, 2)
	require.NoError(t, err)

	level, err := svc.SetInventoryLevel(ctx, boItem, boLocA, 3)
	require.NoError(t, err)

	filled := store.backorder(waiting.ID)
	assert.Equal(t, models.BackorderFilled, filled.Status)
	assert.Equal(t, int64(3), store.reservation(filled.ReservationID).Quantity)
	assert.Equal(t, int64(0), level.StockedQuantity)
}

// TestSettleWithdrawsUpToTheWindow: the withdrawal only grows, a redelivered
// smaller window leaves it, a filled claim answers what was withdrawn before
// the fill and where it was filled, a line with no claim answers nothing, and a
// bundle's part withdraws its units per line unit.
func TestSettleWithdrawsUpToTheWindow(t *testing.T) {
	ctx := context.Background()

	t.Run("waiting", func(t *testing.T) {
		svc, store := backorderService(t)
		waiting := claim(t, svc, "oli_1", 5, boLocA)

		settled, err := svc.SettleBackorder(ctx, "oli_1", 5, 2)
		require.NoError(t, err)
		assert.Equal(t, map[string]int64{boItem: 5}, settled.Undeducted)
		assert.Empty(t, settled.FilledAt)
		assert.Equal(t, int64(2), store.backorder(waiting.ID).WithdrawnQuantity)

		_, err = svc.SettleBackorder(ctx, "oli_1", 5, 3)
		require.NoError(t, err)
		_, err = svc.SettleBackorder(ctx, "oli_1", 5, 2)
		require.NoError(t, err)
		assert.Equal(t, int64(3), store.backorder(waiting.ID).WithdrawnQuantity, "a redelivery does not shrink it")

		_, err = svc.SettleBackorder(ctx, "oli_1", 5, 5)
		require.NoError(t, err)
		assert.Equal(t, models.BackorderWithdrawn, store.backorder(waiting.ID).Status)
	})

	t.Run("filled", func(t *testing.T) {
		svc, _ := backorderService(t)
		claim(t, svc, "oli_1", 5, boLocA)
		_, err := svc.SettleBackorder(ctx, "oli_1", 5, 2)
		require.NoError(t, err)
		_, err = svc.SetInventoryLevel(ctx, boItem, boLocA, 3)
		require.NoError(t, err)

		settled, err := svc.SettleBackorder(ctx, "oli_1", 5, 4)
		require.NoError(t, err)
		assert.Equal(t, map[string]int64{boItem: 2}, settled.Undeducted, "two never left; three did")
		assert.Equal(t, map[string]string{boItem: boLocA}, settled.FilledAt)
	})

	t.Run("no claim", func(t *testing.T) {
		svc, _ := backorderService(t)

		settled, err := svc.SettleBackorder(ctx, "oli_none", 5, 2)
		require.NoError(t, err)
		assert.Empty(t, settled.Undeducted)
		assert.Empty(t, settled.FilledAt)
	})

	t.Run("a bundle's part", func(t *testing.T) {
		svc, store := backorderService(t)
		waiting := claim(t, svc, "oli_box", 10, boLocA)

		_, err := svc.SettleBackorder(ctx, "oli_box", 5, 2)
		require.NoError(t, err)
		assert.Equal(t, int64(4), store.backorder(waiting.ID).WithdrawnQuantity, "two boxes of two")
	})

	t.Run("refusals", func(t *testing.T) {
		svc, _ := backorderService(t)
		claim(t, svc, "oli_odd", 5, boLocA)

		_, err := svc.SettleBackorder(ctx, "oli_odd", 2, 1)
		assert.Equal(t, errors.KindInternal, errors.KindOf(err), "five units are not a whole number per each of two")
		_, err = svc.SettleBackorder(ctx, "oli_odd", 5, 6)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		_, err = svc.SettleBackorder(ctx, "oli_odd", 0, 0)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	})
}

// TestAPartialWithdrawalFillsFromStockOnHand: three units passed over by a
// claim of five complete it once two are written off.
func TestAPartialWithdrawalFillsFromStockOnHand(t *testing.T) {
	ctx := context.Background()
	svc, store := backorderService(t)
	waiting := claim(t, svc, "oli_1", 5, boLocA)
	_, err := svc.SetInventoryLevel(ctx, boItem, boLocA, 3)
	require.NoError(t, err)
	require.Equal(t, models.BackorderWaiting, store.backorder(waiting.ID).Status)

	settled, err := svc.SettleBackorder(ctx, "oli_1", 5, 2)
	require.NoError(t, err)

	got := store.backorder(waiting.ID)
	assert.Equal(t, models.BackorderFilled, got.Status)
	assert.Equal(t, int64(3), store.reservation(got.ReservationID).Quantity)
	assert.Equal(t, int64(0), store.level(boItem, boLocA).StockedQuantity)
	assert.Equal(t, int64(5), settled.Undeducted[boItem],
		"the answer is the one read before the drain; its target is zero either way")
}

// TestASettlementFindsTheLinesClaimsWhateverTheVariantLinks: a line's claims are
// found by the line, each on its own item, so a variant relinked since the
// order still settles what the line was owed.
func TestASettlementFindsTheLinesClaimsWhateverTheVariantLinks(t *testing.T) {
	ctx := context.Background()
	svc, store := backorderService(t)
	store.seedItem("invitem_old", "SKU-OLD")
	_, err := svc.ClaimBackorder(ctx, service.ClaimBackorderInput{
		InventoryItemID: "invitem_old", OrderID: boOrder, OrderLineItemID: "oli_1",
		Quantity: 2, LocationIDs: []string{boLocA},
	})
	require.NoError(t, err)

	settled, err := svc.SettleBackorder(ctx, "oli_1", 2, 2)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"invitem_old": 2}, settled.Undeducted)
}

// TestAClaimIsIdempotentPerLine: a second claim for the line and item is the
// first; one naming another quantity is a conflict.
func TestAClaimIsIdempotentPerLine(t *testing.T) {
	ctx := context.Background()
	svc, store := backorderService(t)
	first := claim(t, svc, "oli_1", 3, boLocA)

	again := claim(t, svc, "oli_1", 3, boLocA)
	assert.Equal(t, first.ID, again.ID)
	assert.Len(t, store.backorders, 1)

	_, err := svc.ClaimBackorder(ctx, service.ClaimBackorderInput{
		InventoryItemID: boItem, OrderID: boOrder, OrderLineItemID: "oli_1", Quantity: 4,
	})
	require.Error(t, err)
	assert.Equal(t, service.CodeBackorderMismatch, errors.CodeOf(err))
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestTheDrainTakesStockInRankOrder: a claim recorded while two warehouses
// already held stock is filled at the one ranked first.
func TestTheDrainTakesStockInRankOrder(t *testing.T) {
	ctx := context.Background()
	svc, store := backorderService(t)
	store.seedLevel(boItem, boLocA, 5, 0)
	store.seedLevel(boItem, boLocB, 5, 0)
	waiting := claim(t, svc, "oli_1", 2, boLocB, boLocA)

	_, err := svc.SettleBackorder(ctx, "oli_1", 2, 0)
	require.NoError(t, err)

	assert.Equal(t, boLocB, store.backorder(waiting.ID).FilledLocationID)
	assert.Equal(t, int64(5), store.level(boItem, boLocA).StockedQuantity)
	assert.Equal(t, int64(3), store.level(boItem, boLocB).StockedQuantity)
}

// TestTheDrainPassesOverAWarehouseThatCannotFillIt: a claim of two ranked first
// at a warehouse that cannot fill it is filled at the next one — whether the
// first holds too little, is closed, or holds no level of the item.
func TestTheDrainPassesOverAWarehouseThatCannotFillIt(t *testing.T) {
	ctx := context.Background()
	const first = "sloc_first"

	for name, seed := range map[string]func(store *fakeStore){
		"too little stock": func(store *fakeStore) {
			store.seedLocation(first)
			store.seedLevel(boItem, first, 1, 0)
		},
		"closed": func(store *fakeStore) {
			store.seedClosedLocation(first)
			store.seedLevel(boItem, first, 5, 0)
		},
		"no level of the item": func(store *fakeStore) { store.seedLocation(first) },
	} {
		t.Run(name, func(t *testing.T) {
			svc, store := backorderService(t)
			seed(store)
			store.seedLevel(boItem, boLocB, 5, 0)
			waiting := claim(t, svc, "oli_1", 2, first, boLocB)

			_, err := svc.SettleBackorder(ctx, "oli_1", 2, 0)
			require.NoError(t, err)

			got := store.backorder(waiting.ID)
			assert.Equal(t, models.BackorderFilled, got.Status)
			assert.Equal(t, boLocB, got.FilledLocationID)
			assert.Equal(t, int64(3), store.level(boItem, boLocB).StockedQuantity)
		})
	}
}

// TestAFillThatFailsAfterTheWithdrawalAnswersAndSaysSo: the withdrawal commits
// before the drain, so a drain that fails leaves the answers true; they come
// back with the error, which the cancellation flow returns so the bus retries.
func TestAFillThatFailsAfterTheWithdrawalAnswersAndSaysSo(t *testing.T) {
	ctx := context.Background()
	svc, store := backorderService(t)
	store.seedLevel(boItem, boLocA, 5, 0)
	waiting := claim(t, svc, "oli_1", 5, boLocA)
	store.failCreateReservation = errors.Internal("test_store_down", "the store is down")

	settled, err := svc.SettleBackorder(ctx, "oli_1", 5, 2)

	require.Error(t, err)
	assert.Equal(t, map[string]int64{boItem: 5}, settled.Undeducted, "the answer stands")
	got := store.backorder(waiting.ID)
	assert.Equal(t, int64(2), got.WithdrawnQuantity, "the withdrawal committed")
	assert.Equal(t, models.BackorderWaiting, got.Status)
}

// TestAnItemOwingOrdersIsNotDeleted: a waiting claim keeps the item; once
// withdrawn it does not.
func TestAnItemOwingOrdersIsNotDeleted(t *testing.T) {
	ctx := context.Background()
	svc, _ := backorderService(t)
	claim(t, svc, "oli_1", 2, boLocA)

	err := svc.DeleteInventoryItem(ctx, boItem)
	require.Error(t, err)
	assert.Equal(t, service.CodeItemOwesOrders, errors.CodeOf(err))

	_, err = svc.SettleBackorder(ctx, "oli_1", 2, 2)
	require.NoError(t, err)
	require.NoError(t, svc.DeleteInventoryItem(ctx, boItem))
}

// TestAQueueReadLocksOnlyClaimsThatFit: the queue is asked for the claims that
// fit what is sellable after the write.
func TestAQueueReadLocksOnlyClaimsThatFit(t *testing.T) {
	ctx := context.Background()
	svc, store := backorderService(t)
	store.seedLevel(boItem, boLocA, 1, 1)
	claim(t, svc, "oli_1", 9, boLocA)

	_, err := svc.AdjustInventory(ctx, boItem, boLocA, 3)
	require.NoError(t, err)

	require.Len(t, store.queueReads, 1)
	assert.Equal(t, queueRead{itemID: boItem, locationID: boLocA, available: 3}, store.queueReads[0])
}

// TestTheBackorderQueueListing: an unknown status is refused and an unknown item
// is not found.
func TestTheBackorderQueueListing(t *testing.T) {
	ctx := context.Background()
	svc, _ := backorderService(t)
	claim(t, svc, "oli_1", 2, boLocA)
	claim(t, svc, "oli_2", 3, boLocA)
	_, err := svc.SettleBackorder(ctx, "oli_2", 3, 3)
	require.NoError(t, err)

	waiting, total, err := svc.ListBackorders(ctx, service.ListBackordersInput{InventoryItemID: boItem, Status: "waiting"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, waiting, 1)
	assert.Equal(t, "oli_1", waiting[0].OrderLineItemID)

	_, _, err = svc.ListBackorders(ctx, service.ListBackordersInput{InventoryItemID: boItem, Status: "late"})
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	_, _, err = svc.ListBackorders(ctx, service.ListBackordersInput{InventoryItemID: "invitem_none"})
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestAFillIsNotTheShelfOfTheOrdersSale: the way back for another line's
// write-off is the checkout's sale, not a claim's fill (ADR 0392).
func TestAFillIsNotTheShelfOfTheOrdersSale(t *testing.T) {
	ctx := context.Background()
	svc, _ := backorderService(t)
	claim(t, svc, "oli_1", 2, boLocB)
	_, err := svc.SetInventoryLevel(ctx, boItem, boLocB, 2)
	require.NoError(t, err)
	_, err = svc.SetInventoryLevel(ctx, boItem, boLocA, 3)
	require.NoError(t, err)
	reserved, err := svc.Reserve(ctx, service.ReserveInput{InventoryItemID: boItem, LocationID: boLocA, Quantity: 1})
	require.NoError(t, err)
	require.NoError(t, svc.ConfirmReservation(ctx, reserved.ID, boOrder))

	shelves, err := svc.SaleLocations(ctx, boOrder)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{boItem: boLocA}, shelves)
}
