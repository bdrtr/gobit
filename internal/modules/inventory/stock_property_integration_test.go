//go:build integration

package inventory_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// promise is the model's copy of one reservation.
type promise struct {
	id       string
	quantity int64
	status   models.ReservationStatus
}

// TestTheCountHoldsUnderAnySequence is ADR 0249 on the stock: any sequence of
// counts, corrections, restocks, reservations, releases and confirmations
// leaves the level where a model of them says, never below what is promised,
// with a ledger that explains the count step by step, and each call is refused
// exactly when the model says it has to be.
//
// Every item is new, so its ledger begins at zero and its deltas add up to its
// count, which is not true of stock older than the ledger (ADR 0068).
func TestTheCountHoldsUnderAnySequence(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	loc := addLocation(ctx, t, svc)

	rapid.Check(t, func(rt *rapid.T) {
		item, err := svc.CreateInventoryItem(ctx, service.CreateInventoryItemInput{
			SKU: "SKU-" + models.NewInventoryItemID(), Title: "property",
		})
		require.NoError(rt, err)
		// The level exists from the first count on; before it, every call is a
		// NotFound, which is a different contract from the one under test.
		stocked := rapid.Int64Range(0, 20).Draw(rt, "first count")
		_, err = svc.SetInventoryLevel(ctx, item.ID, loc.ID, stocked)
		require.NoError(rt, err)

		var reserved int64
		var promises []*promise
		pick := func(rt *rapid.T) *promise {
			if len(promises) == 0 {
				rt.Skip("nothing is reserved yet")
			}
			return promises[rapid.IntRange(0, len(promises)-1).Draw(rt, "reservation")]
		}
		// A refusal is the service's, by the code its contract names. The
		// database's CHECK (reserved <= stocked) refuses the same shapes as a
		// conflict too, and a kind alone would pass with the service's own
		// check gone.
		refused := func(rt *rapid.T, err error, code string) {
			require.Error(rt, err)
			require.True(rt, errors.HasKind(err, errors.KindConflict), "%v", err)
			require.Equal(rt, code, errors.CodeOf(err), "%v", err)
		}

		rt.Repeat(map[string]func(*rapid.T){
			"count": func(rt *rapid.T) {
				count := rapid.Int64Range(0, 20).Draw(rt, "count")
				_, err := svc.SetInventoryLevel(ctx, item.ID, loc.ID, count)
				if count < reserved {
					refused(rt, err, service.CodeInsufficientStock)
					return
				}
				require.NoError(rt, err)
				stocked = count
			},
			"correct": func(rt *rapid.T) {
				delta := rapid.Int64Range(-10, 10).Filter(func(d int64) bool { return d != 0 }).Draw(rt, "delta")
				_, err := svc.AdjustInventory(ctx, item.ID, loc.ID, delta)
				if stocked+delta < reserved {
					refused(rt, err, service.CodeInsufficientStock)
					return
				}
				require.NoError(rt, err)
				stocked += delta
			},
			"restock": func(rt *rapid.T) {
				quantity := rapid.Int64Range(1, 5).Draw(rt, "restocked")
				_, err := svc.RestockInventory(ctx, item.ID, loc.ID, quantity)
				require.NoError(rt, err)
				stocked += quantity
			},
			"reserve": func(rt *rapid.T) {
				quantity := rapid.Int64Range(1, 5).Draw(rt, "reserved")
				reservation, err := svc.Reserve(ctx, service.ReserveInput{
					InventoryItemID: item.ID, LocationID: loc.ID, Quantity: quantity,
				})
				if quantity > stocked-reserved {
					refused(rt, err, service.CodeInsufficientStock)
					return
				}
				require.NoError(rt, err)
				reserved += quantity
				promises = append(promises, &promise{id: reservation.ID, quantity: quantity, status: models.ReservationActive})
			},
			"release": func(rt *rapid.T) {
				p := pick(rt)
				err := svc.ReleaseReservation(ctx, p.id)
				switch p.status {
				case models.ReservationConfirmed:
					refused(rt, err, service.CodeReservationNotActive)
				case models.ReservationReleased:
					require.NoError(rt, err, "a second release is a no-op")
				default:
					require.NoError(rt, err)
					reserved -= p.quantity
					p.status = models.ReservationReleased
				}
			},
			"confirm": func(rt *rapid.T) {
				p := pick(rt)
				err := svc.ConfirmReservation(ctx, p.id, testSaleOrderID)
				switch p.status {
				case models.ReservationReleased:
					refused(rt, err, service.CodeReservationNotActive)
				case models.ReservationConfirmed:
					require.NoError(rt, err, "a second confirmation is a no-op")
				default:
					require.NoError(rt, err)
					stocked -= p.quantity
					reserved -= p.quantity
					p.status = models.ReservationConfirmed
				}
			},
			"": func(rt *rapid.T) {
				requireCountHolds(ctx, rt, svc, item.ID, stocked, reserved)
			},
		})
	})
}

// requireCountHolds reads the level and the whole ledger of a new item and holds
// them to the model's count.
func requireCountHolds(ctx context.Context, rt *rapid.T, svc *service.Service, itemID string, stocked, reserved int64) {
	levels, err := svc.ListInventoryLevels(ctx, itemID)
	require.NoError(rt, err)
	require.Len(rt, levels, 1)
	level := levels[0]
	require.Equal(rt, stocked, level.StockedQuantity, "the count")
	require.Equal(rt, reserved, level.ReservedQuantity, "the reserved units are the live promises")
	require.GreaterOrEqual(rt, level.StockedQuantity-level.ReservedQuantity, int64(0), "nothing is promised that is not there")

	// The ledger, newest first: each row's count is the one after it plus its
	// change, the newest is the level's, and the oldest starts from nothing.
	var ledger []models.Movement
	after := page.Cursor{}
	for {
		batch, err := svc.ListMovements(ctx, service.ListMovementsInput{InventoryItemID: itemID, After: after, Limit: 100})
		require.NoError(rt, err)
		ledger = append(ledger, batch...)
		if len(batch) < 100 {
			break
		}
		last := batch[len(batch)-1]
		after = page.Cursor{Time: last.CreatedAt, ID: last.ID}
	}
	if len(ledger) == 0 {
		require.Zero(rt, stocked, "a count that moved has a ledger")
		return
	}
	require.Equal(rt, stocked, ledger[0].StockedAfter, "the newest movement names the count")
	for i := range ledger {
		require.NotZero(rt, ledger[i].Delta)
		before := ledger[i].StockedAfter - ledger[i].Delta
		if i+1 < len(ledger) {
			require.Equal(rt, ledger[i+1].StockedAfter, before, "movement %s follows the one before it", ledger[i].ID)
		} else {
			require.Zero(rt, before, "the ledger of a new item starts at zero")
		}
	}
}
