//go:build integration

// A backordered line waits for its units (ADR 0392), against a real PostgreSQL.
//
// The unit lane proves the decisions on a fake store. What only the database can
// prove is that the fill commits with the write that made the units sellable,
// that arrivals racing for one claim — at one warehouse or at two it names — fill
// it once, that a claim racing the item's deletion finds the item deleted, that
// the schema refuses a claim that cannot be true, that the queue read locks only
// the claims that fit, and that a fill is not taken for the shelf another line
// of the order sold from.
package inventory_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// newClaim records a waiting claim on the item for a fresh line of the order.
func newClaim(
	ctx context.Context, t *testing.T, svc *service.Service,
	itemID, orderID string, quantity int64, locations ...string,
) models.Backorder {
	t.Helper()

	claim, err := svc.ClaimBackorder(ctx, service.ClaimBackorderInput{
		InventoryItemID: itemID, OrderID: orderID, OrderLineItemID: "oli_" + models.NewBackorderID(),
		Quantity: quantity, LocationIDs: locations,
	})
	require.NoError(t, err)

	return claim
}

// claimOf reads one claim back by its line.
func claimOf(ctx context.Context, t *testing.T, svc *service.Service, itemID, id string) models.Backorder {
	t.Helper()

	claims, _, err := svc.ListBackorders(ctx, service.ListBackordersInput{InventoryItemID: itemID})
	require.NoError(t, err)
	for i := range claims {
		if claims[i].ID == id {
			return claims[i]
		}
	}
	t.Fatalf("claim %s is not listed", id)

	return models.Backorder{}
}

// TestAnArrivalFillsAWaitingClaim is I1: a count of five where an order waits
// for two answers three, the ledger shows the count and then a sale naming the
// claim's reservation and the order, and the claim names its shelf.
func TestAnArrivalFillsAWaitingClaim(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := addItem(ctx, t, svc), addLocation(ctx, t, svc)
	claim := newClaim(ctx, t, svc, item.ID, testSaleOrderID, 2, loc.ID)

	level, err := svc.SetInventoryLevel(ctx, item.ID, loc.ID, 5)
	require.NoError(t, err)
	assert.Equal(t, int64(3), level.StockedQuantity, "the write answers the count after the fill")
	assert.Equal(t, int64(0), level.ReservedQuantity)

	filled := claimOf(ctx, t, svc, item.ID, claim.ID)
	assert.Equal(t, models.BackorderFilled, filled.Status)
	assert.Equal(t, loc.ID, filled.FilledLocationID)
	reservation, err := svc.GetReservation(ctx, filled.ReservationID)
	require.NoError(t, err)
	assert.Equal(t, models.ReservationConfirmed, reservation.Status)
	assert.Equal(t, claim.OrderLineItemID, reservation.LineItemID)

	ledger, err := svc.ListMovements(ctx, service.ListMovementsInput{InventoryItemID: item.ID})
	require.NoError(t, err)
	require.Len(t, ledger, 2)
	assert.Equal(t, models.MovementSale, ledger[0].Reason, "newest first")
	assert.Equal(t, int64(-2), ledger[0].Delta)
	assert.Equal(t, filled.ReservationID, ledger[0].ReservationID)
	assert.Equal(t, testSaleOrderID, ledger[0].Reference)
	assert.Equal(t, models.MovementStockCount, ledger[1].Reason)
	assert.Equal(t, int64(5), ledger[1].Delta)
}

// TestRacingArrivalsFillAClaimOnce is I2: six single-unit arrivals at once for a
// claim of three fill it exactly once, and the other three units stay on sale.
func TestRacingArrivalsFillAClaimOnce(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := withStock(ctx, t, svc, 0)
	claim := newClaim(ctx, t, svc, item.ID, testSaleOrderID, 3, loc.ID)

	const arrivals = 6
	var wg sync.WaitGroup
	errs := make(chan error, arrivals)
	for range arrivals {
		wg.Go(func() {
			_, err := svc.AdjustInventory(ctx, item.ID, loc.ID, 1)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	assert.Equal(t, models.BackorderFilled, claimOf(ctx, t, svc, item.ID, claim.ID).Status)
	assert.Equal(t, int64(3), stockedQuantityOf(ctx, t, item.ID, loc.ID), "six arrived, three went to the order")

	var sales int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM inventory_movements WHERE inventory_item_id = $1 AND reason = 'sale'`,
		item.ID).Scan(&sales))
	assert.Equal(t, 1, sales, "one fill, one sale")
}

// lockWaiters counts the sessions waiting on a lock, other than this probe.
func lockWaiters(ctx context.Context) (int64, error) {
	var waiting int64
	err := testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM pg_stat_activity
         WHERE wait_event_type = 'Lock' AND query NOT ILIKE '%pg_stat_activity%'`).Scan(&waiting)

	return waiting, err
}

// TestArrivalsAtTwoWarehousesFillAClaimOnce is I2b: a claim of two names two
// warehouses, and five units arrive at each while the claim's row is held, so
// both writes reach the claim before either has filled it. The claim's own lock
// makes the second write find it filled and pass it over: both arrivals
// succeed, one sale is written, and the other warehouse keeps its five. Nothing
// else orders them — they take different level locks and share the item's. (A
// count would not do: it takes the item's lock exclusively.)
func TestArrivalsAtTwoWarehousesFillAClaimOnce(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item := addItem(ctx, t, svc)
	east, west := addLocation(ctx, t, svc), addLocation(ctx, t, svc)
	for _, loc := range []string{east.ID, west.ID} {
		_, err := svc.SetInventoryLevel(ctx, item.ID, loc, 0)
		require.NoError(t, err)
	}
	claim := newClaim(ctx, t, svc, item.ID, testSaleOrderID, 2, east.ID, west.ID)

	hold, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = hold.Rollback(ctx) }()
	_, err = hold.Exec(ctx, `SELECT id FROM inventory_backorders WHERE id = $1 FOR UPDATE`, claim.ID)
	require.NoError(t, err)

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, loc := range []string{east.ID, west.ID} {
		wg.Go(func() {
			_, writeErr := svc.AdjustInventory(ctx, item.ID, loc, 5)
			errs <- writeErr
		})
	}
	require.Eventually(t, func() bool {
		waiting, probeErr := lockWaiters(ctx)
		return probeErr == nil && waiting >= 2
	}, 10*time.Second, 20*time.Millisecond, "both arrivals reach the claim")

	require.NoError(t, hold.Rollback(ctx))
	wg.Wait()
	close(errs)
	for writeErr := range errs {
		require.NoError(t, writeErr, "an arrival where the claim was filled elsewhere passes it over")
	}

	filled := claimOf(ctx, t, svc, item.ID, claim.ID)
	require.Equal(t, models.BackorderFilled, filled.Status)
	var sales int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM inventory_movements WHERE inventory_item_id = $1 AND reason = 'sale'`,
		item.ID).Scan(&sales))
	assert.Equal(t, 1, sales, "one fill, one sale")
	other := map[string]string{east.ID: west.ID, west.ID: east.ID}[filled.FilledLocationID]
	assert.Equal(t, int64(3), stockedQuantityOf(ctx, t, item.ID, filled.FilledLocationID))
	assert.Equal(t, int64(5), stockedQuantityOf(ctx, t, item.ID, other), "the other arrival stays on sale")
}

// TestAClaimRacingADeletionFindsTheItemDeleted is I6: the deletion holds the
// item's lock, has counted no waiting claim and deletes the item; a claim
// recorded meanwhile waits on the item's lock and then finds it deleted. The
// foreign key would wait as well, and then let the claim in. The rival holds
// the lock and writes the deletion's two statements itself, because the
// deletion has no seam between its count and its write.
func TestAClaimRacingADeletionFindsTheItemDeleted(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := addItem(ctx, t, svc), addLocation(ctx, t, svc)

	deletion, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = deletion.Rollback(ctx) }()
	_, err = deletion.Exec(ctx, `SELECT id FROM inventory_items WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, item.ID)
	require.NoError(t, err)

	claimed := make(chan error, 1)
	go func() {
		_, claimErr := svc.ClaimBackorder(ctx, service.ClaimBackorderInput{
			InventoryItemID: item.ID, OrderID: testSaleOrderID, OrderLineItemID: "oli_racing_a_deletion",
			Quantity: 1, LocationIDs: []string{loc.ID},
		})
		claimed <- claimErr
	}()
	require.Eventually(t, func() bool {
		waiting, probeErr := lockWaiters(ctx)
		return probeErr == nil && waiting >= 1
	}, 10*time.Second, 20*time.Millisecond, "the claim reaches the item")

	_, err = deletion.Exec(ctx, `UPDATE inventory_items SET deleted_at = now(), updated_at = now() WHERE id = $1`, item.ID)
	require.NoError(t, err)
	require.NoError(t, deletion.Commit(ctx))

	claimErr := <-claimed
	require.Error(t, claimErr, "an item that is gone owes nothing")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(claimErr))
	var rows int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM inventory_backorders WHERE inventory_item_id = $1`, item.ID).Scan(&rows))
	assert.Zero(t, rows, "no claim waits on a deleted item")
}

// TestAWithdrawnClaimIsNotFilled is I3: a claim whose every unit was written off
// is withdrawn, and the next arrival stays on sale.
func TestAWithdrawnClaimIsNotFilled(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := withStock(ctx, t, svc, 0)
	claim := newClaim(ctx, t, svc, item.ID, testSaleOrderID, 4, loc.ID)

	settled, err := svc.SettleBackorder(ctx, claim.OrderLineItemID, 4, 4)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{item.ID: 4}, settled.Undeducted, "no unit of the line ever left")
	assert.Equal(t, models.BackorderWithdrawn, claimOf(ctx, t, svc, item.ID, claim.ID).Status)

	level, err := svc.RestockInventory(ctx, item.ID, loc.ID, 4)
	require.NoError(t, err)
	assert.Equal(t, int64(4), level.Available(), "a withdrawn claim takes nothing")
}

// TestTheSchemaRefusesAClaimThatCannotBeTrue is I4: each CHECK and both unique
// indexes refuse the row they mean, by name, and each row breaks that rule
// alone.
func TestTheSchemaRefusesAClaimThatCannotBeTrue(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := addItem(ctx, t, svc), addLocation(ctx, t, svc)
	_, err := svc.SetInventoryLevel(ctx, item.ID, loc.ID, 10)
	require.NoError(t, err)
	reservation, err := svc.Reserve(ctx, service.ReserveInput{InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1})
	require.NoError(t, err)
	named, err := svc.Reserve(ctx, service.ReserveInput{InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1})
	require.NoError(t, err)
	taken := newClaim(ctx, t, svc, item.ID, testSaleOrderID, 1, loc.ID)

	insert := `INSERT INTO inventory_backorders
	    (id, inventory_item_id, order_id, order_line_item_id, quantity, withdrawn_quantity,
	     location_ids, status, reservation_id, filled_location_id)
	    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	// A claim filled by the second reservation, which the last row names again.
	_, err = testPool.Pool().Exec(ctx, insert, models.NewBackorderID(), item.ID, testSaleOrderID, "oli_filled",
		1, 0, []string{loc.ID}, "filled", named.ID, loc.ID)
	require.NoError(t, err, "a filled claim naming its reservation and shelf is a row the schema takes")
	none := (*string)(nil)
	cases := []struct {
		name       string
		line       string
		quantity   int64
		withdrawn  int64
		locations  []string
		status     string
		reserved   *string
		filledAt   *string
		constraint string
	}{
		{"a claim of nothing", "oli_a", 0, 0, []string{}, "withdrawn", none, none,
			`check constraint "inventory_backorders_quantity_positive"`},
		{"more withdrawn than owed", "oli_b", 2, 3, []string{}, "waiting", none, none,
			`check constraint "inventory_backorders_withdrawn_bounded"`},
		{"a status nobody defined", "oli_c", 2, 0, []string{}, "late", none, none,
			`check constraint "inventory_backorders_status_valid"`},
		{"a fill naming no reservation", "oli_d", 2, 0, []string{}, "filled", none, none,
			`check constraint "inventory_backorders_filled_names_its_reservation"`},
		{"a fill naming no shelf", "oli_e", 2, 0, []string{}, "filled", &reservation.ID, none,
			`check constraint "inventory_backorders_filled_names_its_location"`},
		{"withdrawn while owing", "oli_f", 2, 1, []string{}, "withdrawn", none, none,
			`check constraint "inventory_backorders_withdrawn_is_whole"`},
		{"a blank line", " ", 2, 0, []string{}, "waiting", none, none,
			`check constraint "inventory_backorders_ids_not_blank"`},
		{"a warehouse that is no warehouse", "oli_g", 2, 0, []string{loc.ID, ""}, "waiting", none, none,
			""},
		{"a second claim of the line", taken.OrderLineItemID, 1, 0, []string{}, "waiting", none, none,
			`"inventory_backorders_line_uniq"`},
		{"a second claim of the reservation", "oli_h", 1, 0, []string{loc.ID}, "filled", &named.ID, &loc.ID,
			`"inventory_backorders_reservation_uniq"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			locations := any(tc.locations)
			want := tc.constraint
			if want == "" {
				// A NULL inside the array: pgx cannot write one from []string.
				locations = []*string{&loc.ID, nil}
				want = `check constraint "inventory_backorders_locations_not_null"`
			}
			_, err := testPool.Pool().Exec(ctx, insert,
				models.NewBackorderID(), item.ID, testSaleOrderID, tc.line, tc.quantity, tc.withdrawn,
				locations, tc.status, tc.reserved, tc.filledAt)

			require.Error(t, err, "the schema has to refuse this row")
			assert.Contains(t, err.Error(), want)
		})
	}
}

// TestSaleLocationsAnswerTheCheckoutsSale is I5: a claim of the order filled at
// one warehouse before the checkout confirmed another line at a second is not
// the shelf a write-off of that line goes back to.
func TestSaleLocationsAnswerTheCheckoutsSale(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item := addItem(ctx, t, svc)
	sold, filledAt := addLocation(ctx, t, svc), addLocation(ctx, t, svc)
	const order = "order_01SALELOCATIONSFILL0"

	newClaim(ctx, t, svc, item.ID, order, 2, filledAt.ID)
	_, err := svc.SetInventoryLevel(ctx, item.ID, filledAt.ID, 2)
	require.NoError(t, err)
	_, err = svc.SetInventoryLevel(ctx, item.ID, sold.ID, 3)
	require.NoError(t, err)
	reservation, err := svc.Reserve(ctx, service.ReserveInput{InventoryItemID: item.ID, LocationID: sold.ID, Quantity: 1})
	require.NoError(t, err)
	require.NoError(t, svc.ConfirmReservation(ctx, reservation.ID, order))

	shelves, err := svc.SaleLocations(ctx, order)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{item.ID: sold.ID}, shelves)
}

// TestTheQueueReadLocksOnlyTheClaimsThatFit is the fit predicate on real SQL:
// with three units sellable, a claim of five is not locked and a claim of two
// is, and a claim naming another warehouse is not.
func TestTheQueueReadLocksOnlyTheClaimsThatFit(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item := addItem(ctx, t, svc)
	here, there := addLocation(ctx, t, svc), addLocation(ctx, t, svc)
	newClaim(ctx, t, svc, item.ID, testSaleOrderID, 5, here.ID)
	fits := newClaim(ctx, t, svc, item.ID, testSaleOrderID, 2, there.ID, here.ID)
	newClaim(ctx, t, svc, item.ID, testSaleOrderID, 1, there.ID)

	repo := newRepo()
	var locked []models.Backorder
	require.NoError(t, repo.WithTx(ctx, func(ctx context.Context) error {
		var err error
		locked, err = repo.LockWaitingBackordersAt(ctx, item.ID, here.ID, 3)
		return err
	}))

	require.Len(t, locked, 1)
	assert.Equal(t, fits.ID, locked[0].ID)
}
