//go:build integration

// Stock on its way (ADR 0399), against a real PostgreSQL.
//
// The unit lane proves the decisions on a fake store. What only the database can
// prove is that the schema refuses a receipt and a movement that cannot be true,
// that a receive waits on a cancel holding the receipt and then refuses, that a
// receive or a cancel waiting on the same act finishes with its answer, that two
// receipts opening one level serialize on the item, that the listing orders and
// filters, that a close counts a receipt recorded while it waited, that a
// deleted item is owed nothing, that the reverse migration keeps the ledger's
// arithmetic, that the close's and the deletion's counts read only what is
// expected where they are asked, that the receipt copies its movement's count
// and moment, that the forecast reads only what is still expected and still
// waiting, and that the forecast is what receiving the receipts does.
package inventory_test

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/repository"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
	"github.com/bdrtr/gobit/internal/testdb"
)

// expectedIn is a moment the units are expected, hours ahead.
func expectedIn(hours int) time.Time {
	return time.Now().UTC().Add(time.Duration(hours) * time.Hour).Truncate(time.Microsecond)
}

// helperT is what newReceipt needs of a test: a *testing.T or a *rapid.T.
type helperT interface {
	require.TestingT
	Helper()
}

// newReceipt records an expected receipt of quantity at the location.
func newReceipt(
	ctx context.Context, t helperT, svc *service.Service, itemID, locationID string, quantity int64, at time.Time,
) models.SupplierReceipt {
	t.Helper()

	receipt, err := svc.RecordSupplierReceipt(ctx, service.RecordSupplierReceiptInput{
		InventoryItemID: itemID, LocationID: locationID, Quantity: quantity, ExpectedAt: at,
	})
	require.NoError(t, err)

	return receipt
}

// supplierMovementsOf counts the item's supplier_receipt movements.
func supplierMovementsOf(ctx context.Context, t *testing.T, itemID string) int {
	t.Helper()

	var n int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM inventory_movements WHERE inventory_item_id = $1 AND reason = 'supplier_receipt'`,
		itemID).Scan(&n))

	return n
}

// TestTheSchemaRefusesAReceiptThatCannotBeTrue is I1: one row per CHECK,
// including the half-set rows a single equivalence over two columns would let
// through, each refused by the constraint that means it; and a supplier_receipt
// movement that subtracts, names nothing, or is the second for its receipt.
func TestTheSchemaRefusesAReceiptThatCannotBeTrue(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := addItem(ctx, t, svc), addLocation(ctx, t, svc)
	_, err := svc.SetInventoryLevel(ctx, item.ID, loc.ID, 10)
	require.NoError(t, err)

	insert := `INSERT INTO inventory_supplier_receipts
	    (id, inventory_item_id, location_id, quantity, expected_at, reference, status,
	     received_quantity, received_at, canceled_at)
	    VALUES ($1, $2, $3, $4, now(), $5, $6, $7, $8, $9)`
	at := time.Now().UTC()
	count := func(n int64) *int64 { return &n }
	none := (*int64)(nil)
	noMoment := (*time.Time)(nil)
	noText := (*string)(nil)
	blank := "  "
	cases := []struct {
		name       string
		quantity   int64
		reference  *string
		status     string
		received   *int64
		receivedAt *time.Time
		canceledAt *time.Time
		constraint string
	}{
		{"a receipt of nothing", 0, noText, "expected", none, noMoment, noMoment,
			"inventory_supplier_receipts_quantity_positive"},
		{"a status nobody defined", 1, noText, "late", none, noMoment, noMoment,
			"inventory_supplier_receipts_status_valid"},
		{"received with no count", 1, noText, "received", none, &at, noMoment,
			"inventory_supplier_receipts_received_names_its_count"},
		{"received with no moment", 1, noText, "received", count(1), noMoment, noMoment,
			"inventory_supplier_receipts_received_names_its_moment"},
		{"expected with only a count", 1, noText, "expected", count(1), noMoment, noMoment,
			"inventory_supplier_receipts_received_names_its_count"},
		{"expected with only a moment", 1, noText, "expected", none, &at, noMoment,
			"inventory_supplier_receipts_received_names_its_moment"},
		{"canceled with a count", 1, noText, "canceled", count(1), noMoment, &at,
			"inventory_supplier_receipts_received_names_its_count"},
		{"received with nothing counted", 1, noText, "received", count(0), &at, noMoment,
			"inventory_supplier_receipts_received_positive"},
		{"canceled with no moment", 1, noText, "canceled", none, noMoment, noMoment,
			"inventory_supplier_receipts_canceled_names_its_moment"},
		{"expected and canceled", 1, noText, "expected", none, noMoment, &at,
			"inventory_supplier_receipts_canceled_names_its_moment"},
		{"a blank reference", 1, &blank, "expected", none, noMoment, noMoment,
			"inventory_supplier_receipts_reference_not_blank"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx, insert, models.NewSupplierReceiptID(), item.ID, loc.ID,
				tc.quantity, tc.reference, tc.status, tc.received, tc.receivedAt, tc.canceledAt)

			require.Error(t, err, "the schema has to refuse this row")
			assert.Contains(t, err.Error(), `check constraint "`+tc.constraint+`"`)
		})
	}

	movement := `INSERT INTO inventory_movements
	    (id, inventory_item_id, location_id, reason, delta, stocked_after, reference)
	    VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err = testPool.Pool().Exec(ctx, movement, models.NewMovementID(), item.ID, loc.ID,
		"supplier_receipt", 2, 12, "invsup_once")
	require.NoError(t, err, "a supplier receipt adding units and naming its receipt is a row the schema takes")

	refused := []struct {
		name       string
		delta      int64
		reference  *string
		constraint string
	}{
		{"a receipt that subtracts", -1, ptrTo("invsup_minus"), `check constraint "inventory_movements_supplier_receipt_adds"`},
		{"a receipt naming nothing", 1, noText, `check constraint "inventory_movements_supplier_receipt_names_its_receipt"`},
		{"a second row for one receipt", 1, ptrTo("invsup_once"), `"inventory_movements_supplier_receipt_once_idx"`},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx, movement, models.NewMovementID(), item.ID, loc.ID,
				"supplier_receipt", tc.delta, 10+tc.delta, tc.reference)

			require.Error(t, err, "the schema has to refuse this row")
			assert.Contains(t, err.Error(), tc.constraint)
		})
	}
}

// ptrTo returns the address of the value.
func ptrTo[T any](v T) *T { return &v }

// TestAReceiveWaitsForACancelHoldingTheReceipt is I2: a cancel holds the
// receipt's row and commits; the receive that reached it meanwhile reads it
// again under its own lock and refuses, with nothing written.
func TestAReceiveWaitsForACancelHoldingTheReceipt(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := withStock(ctx, t, svc, 0)
	receipt := newReceipt(ctx, t, svc, item.ID, loc.ID, 3, expectedIn(24))

	cancel, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = cancel.Rollback(ctx) }()
	_, err = cancel.Exec(ctx, `SELECT id FROM inventory_supplier_receipts WHERE id = $1 FOR UPDATE`, receipt.ID)
	require.NoError(t, err)
	_, err = cancel.Exec(ctx, `UPDATE inventory_supplier_receipts
	    SET status = 'canceled', canceled_at = clock_timestamp() WHERE id = $1`, receipt.ID)
	require.NoError(t, err)

	received := make(chan error, 1)
	go func() {
		_, _, receiveErr := svc.ReceiveSupplierReceipt(ctx, item.ID, receipt.ID, 3)
		received <- receiveErr
	}()
	require.Eventually(t, func() bool {
		waiting, probeErr := lockWaiters(ctx)
		return probeErr == nil && waiting >= 1
	}, 10*time.Second, 20*time.Millisecond, "the receive reaches the receipt")
	require.NoError(t, cancel.Commit(ctx))

	receiveErr := <-received
	require.Error(t, receiveErr, "a receipt canceled while the receive waited brings nothing")
	assert.Equal(t, service.CodeSupplierReceiptNotExpected, errors.CodeOf(receiveErr), "%v", receiveErr)
	assert.Equal(t, errors.KindConflict, errors.KindOf(receiveErr))
	assert.Equal(t, int64(0), stockedQuantityOf(ctx, t, item.ID, loc.ID))
	assert.Zero(t, supplierMovementsOf(ctx, t, item.ID))
}

// heldReceipt opens a transaction that holds the receipt's row and moves it
// out of expected with the SQL given, as a first request in flight would.
func heldReceipt(ctx context.Context, t *testing.T, receiptID, move string) pgx.Tx {
	t.Helper()

	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	_, err = tx.Exec(ctx, `SELECT id FROM inventory_supplier_receipts WHERE id = $1 FOR UPDATE`, receiptID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, move, receiptID)
	require.NoError(t, err)

	return tx
}

// awaitLockWaiter waits until a call is blocked on a lock.
func awaitLockWaiter(ctx context.Context, t *testing.T, what string) {
	t.Helper()

	require.Eventually(t, func() bool {
		waiting, probeErr := lockWaiters(ctx)
		return probeErr == nil && waiting >= 1
	}, 10*time.Second, 20*time.Millisecond, what)
}

// TestARepeatWaitingOnTheFirstReceiveFinishes: a receive holds the receipt
// and receives it with 3; the same receive, retried meanwhile, read it expected,
// waits on its lock and then finds it received with its own count. It answers
// the receipt and the level, as a repeat after the first committed does, and
// writes nothing.
func TestARepeatWaitingOnTheFirstReceiveFinishes(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := withStock(ctx, t, svc, 0)
	receipt := newReceipt(ctx, t, svc, item.ID, loc.ID, 3, expectedIn(24))

	first := heldReceipt(ctx, t, receipt.ID, `UPDATE inventory_supplier_receipts
	    SET status = 'received', received_quantity = 3, received_at = clock_timestamp() WHERE id = $1`)

	type answer struct {
		receipt models.SupplierReceipt
		level   *models.InventoryLevel
		err     error
	}
	answered := make(chan answer, 1)
	go func() {
		got, level, err := svc.ReceiveSupplierReceipt(ctx, item.ID, receipt.ID, 3)
		answered <- answer{got, level, err}
	}()
	awaitLockWaiter(ctx, t, "the repeat reaches the receipt")
	require.NoError(t, first.Commit(ctx))

	got := <-answered
	require.NoError(t, got.err, "a repeat of what happened is not a conflict")
	assert.Equal(t, models.SupplierReceiptReceived, got.receipt.Status)
	assert.Equal(t, int64(3), got.receipt.ReceivedQuantity)
	require.NotNil(t, got.level, "the level is answered, as on any repeat")
	assert.Equal(t, loc.ID, got.level.LocationID)
	assert.Zero(t, supplierMovementsOf(ctx, t, item.ID), "the repeat writes nothing")
}

// TestARepeatWaitingOnTheFirstCancelFinishes: a cancel holds the receipt and
// cancels it; the same cancel, retried meanwhile, waits on its lock and then
// answers the canceled receipt.
func TestARepeatWaitingOnTheFirstCancelFinishes(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := withStock(ctx, t, svc, 0)
	receipt := newReceipt(ctx, t, svc, item.ID, loc.ID, 3, expectedIn(24))

	first := heldReceipt(ctx, t, receipt.ID, `UPDATE inventory_supplier_receipts
	    SET status = 'canceled', canceled_at = clock_timestamp() WHERE id = $1`)

	answered := make(chan error, 1)
	var got models.SupplierReceipt
	go func() {
		var err error
		got, err = svc.CancelSupplierReceipt(ctx, item.ID, receipt.ID)
		answered <- err
	}()
	awaitLockWaiter(ctx, t, "the repeat reaches the receipt")
	require.NoError(t, first.Commit(ctx))

	require.NoError(t, <-answered, "a repeat of what happened is not a conflict")
	assert.Equal(t, models.SupplierReceiptCanceled, got.Status)
}

// barrierStore holds every caller that finds no level for a moment, so two
// receipts that both found none meet before either opens it.
type barrierStore struct {
	service.Store
	arrived atomic.Int32
	met     chan struct{}
	once    sync.Once
}

// LockInventoryLevel waits, after a NotFound, until a second caller found none
// too or 200 ms passed.
func (b *barrierStore) LockInventoryLevel(ctx context.Context, itemID, locationID string) (models.InventoryLevel, error) {
	level, err := b.Store.LockInventoryLevel(ctx, itemID, locationID)
	if errors.HasKind(err, errors.KindNotFound) {
		if b.arrived.Add(1) >= 2 {
			b.once.Do(func() { close(b.met) })
		}
		select {
		case <-b.met:
		case <-time.After(200 * time.Millisecond):
		}
	}

	return level, err
}

// TestTwoReceiptsOpenOneLevel is I3: two receipts at a warehouse with no level,
// received at once. The item's lock is exclusive, so the second finds the level
// the first opened: one level holds both counts, explained by two movements.
func TestTwoReceiptsOpenOneLevel(t *testing.T) {
	ctx := context.Background()
	store := &barrierStore{Store: repository.New(testPool.Pool()), met: make(chan struct{})}
	svc := service.New(store, nil)
	item, loc := addItem(ctx, t, svc), addLocation(ctx, t, svc)
	first := newReceipt(ctx, t, svc, item.ID, loc.ID, 3, expectedIn(24))
	second := newReceipt(ctx, t, svc, item.ID, loc.ID, 5, expectedIn(24))

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, r := range []models.SupplierReceipt{first, second} {
		wg.Go(func() {
			_, _, err := svc.ReceiveSupplierReceipt(ctx, item.ID, r.ID, r.Quantity)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err, "both receipts are received")
	}

	assert.Equal(t, int64(8), stockedQuantityOf(ctx, t, item.ID, loc.ID))
	assert.Equal(t, 2, supplierMovementsOf(ctx, t, item.ID))
}

// TestTheReceiptsAreListedByWhenTheyAreExpectedOnRealSQL is I4: three expected
// receipts recorded out of order, one received and one canceled; the filter
// keeps the three, by date, and no filter keeps all five, by date.
func TestTheReceiptsAreListedByWhenTheyAreExpectedOnRealSQL(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := withStock(ctx, t, svc, 0)
	d3 := newReceipt(ctx, t, svc, item.ID, loc.ID, 1, expectedIn(72))
	d1 := newReceipt(ctx, t, svc, item.ID, loc.ID, 1, expectedIn(24))
	d2 := newReceipt(ctx, t, svc, item.ID, loc.ID, 1, expectedIn(48))
	received := newReceipt(ctx, t, svc, item.ID, loc.ID, 1, expectedIn(12))
	canceled := newReceipt(ctx, t, svc, item.ID, loc.ID, 1, expectedIn(60))
	_, _, err := svc.ReceiveSupplierReceipt(ctx, item.ID, received.ID, 1)
	require.NoError(t, err)
	_, err = svc.CancelSupplierReceipt(ctx, item.ID, canceled.ID)
	require.NoError(t, err)

	expected, total, err := svc.ListSupplierReceipts(ctx, service.ListSupplierReceiptsInput{
		InventoryItemID: item.ID, Status: "expected",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Equal(t, []string{d1.ID, d2.ID, d3.ID}, idsOf(expected))

	all, total, err := svc.ListSupplierReceipts(ctx, service.ListSupplierReceiptsInput{InventoryItemID: item.ID})
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Equal(t, []string{received.ID, d1.ID, d2.ID, canceled.ID, d3.ID}, idsOf(all))
}

// idsOf lists the receipts' ids in order.
func idsOf(receipts []models.SupplierReceipt) []string {
	out := make([]string, 0, len(receipts))
	for i := range receipts {
		out = append(out, receipts[i].ID)
	}

	return out
}

// TestARecordingRacingACloseFindsItClosed is I5: the close holds the location
// and stamps it; a receipt recorded meanwhile waits on the location and then
// finds it closed, so no row is written that the close never counted.
func TestARecordingRacingACloseFindsItClosed(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := addItem(ctx, t, svc), addLocation(ctx, t, svc)

	closing, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = closing.Rollback(ctx) }()
	_, err = closing.Exec(ctx, `SELECT id FROM stock_locations WHERE id = $1 FOR UPDATE`, loc.ID)
	require.NoError(t, err)

	recorded := make(chan error, 1)
	go func() {
		_, recordErr := svc.RecordSupplierReceipt(ctx, service.RecordSupplierReceiptInput{
			InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 2, ExpectedAt: expectedIn(24),
		})
		recorded <- recordErr
	}()
	require.Eventually(t, func() bool {
		waiting, probeErr := lockWaiters(ctx)
		return probeErr == nil && waiting >= 1
	}, 10*time.Second, 20*time.Millisecond, "the recording reaches the location")

	_, err = closing.Exec(ctx, `UPDATE stock_locations SET closed_at = now(), updated_at = now() WHERE id = $1`, loc.ID)
	require.NoError(t, err)
	require.NoError(t, closing.Commit(ctx))

	recordErr := <-recorded
	require.Error(t, recordErr, "a closed warehouse is owed nothing")
	assert.Equal(t, service.CodeLocationClosed, errors.CodeOf(recordErr), "%v", recordErr)
	var rows int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM inventory_supplier_receipts WHERE location_id = $1`, loc.ID).Scan(&rows))
	assert.Zero(t, rows)
}

// TestADeletedItemIsOwedNothingOnRealSQL: the foreign key takes a soft-deleted
// item, so the item's lock refuses the receipt -- after the deletion, and when
// the deletion commits while the recording waits on it.
func TestADeletedItemIsOwedNothingOnRealSQL(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	loc := addLocation(ctx, t, svc)
	receiptsOf := func(itemID string) int {
		var rows int
		require.NoError(t, testPool.Pool().QueryRow(ctx,
			`SELECT count(*) FROM inventory_supplier_receipts WHERE inventory_item_id = $1`, itemID).Scan(&rows))
		return rows
	}

	deleted := addItem(ctx, t, svc)
	require.NoError(t, svc.DeleteInventoryItem(ctx, deleted.ID))
	_, err := svc.RecordSupplierReceipt(ctx, service.RecordSupplierReceiptInput{
		InventoryItemID: deleted.ID, LocationID: loc.ID, Quantity: 2, ExpectedAt: expectedIn(24),
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "%v", err)
	assert.Zero(t, receiptsOf(deleted.ID))

	racing := addItem(ctx, t, svc)
	deletion, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = deletion.Rollback(ctx) }()
	_, err = deletion.Exec(ctx, `SELECT id FROM inventory_items WHERE id = $1 FOR UPDATE`, racing.ID)
	require.NoError(t, err)

	recorded := make(chan error, 1)
	go func() {
		_, recordErr := svc.RecordSupplierReceipt(ctx, service.RecordSupplierReceiptInput{
			InventoryItemID: racing.ID, LocationID: loc.ID, Quantity: 2, ExpectedAt: expectedIn(24),
		})
		recorded <- recordErr
	}()
	awaitLockWaiter(ctx, t, "the recording reaches the item")
	_, err = deletion.Exec(ctx, `UPDATE inventory_items SET deleted_at = now() WHERE id = $1`, racing.ID)
	require.NoError(t, err)
	require.NoError(t, deletion.Commit(ctx))

	recordErr := <-recorded
	require.Error(t, recordErr, "an item deleted while the recording waited is owed nothing")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(recordErr), "%v", recordErr)
	assert.Zero(t, receiptsOf(racing.ID))
}

// TestTheForecastReadsWhatIsStillExpectedAndStillWaiting: at a warehouse with
// nothing to sell, a receipt whose moment has passed, a canceled one, a
// received one and a filled claim are all on record; the forecast is the one
// receipt still expected and not yet due, which nothing waiting takes.
func TestTheForecastReadsWhatIsStillExpectedAndStillWaiting(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := withStock(ctx, t, svc, 0)

	_, err := svc.ClaimBackorder(ctx, service.ClaimBackorderInput{
		InventoryItemID: item.ID, OrderID: testSaleOrderID,
		OrderLineItemID: "oli_forecast_filled_" + models.NewBackorderID(),
		Quantity:        2, LocationIDs: []string{loc.ID},
	})
	require.NoError(t, err)
	filling := newReceipt(ctx, t, svc, item.ID, loc.ID, 2, expectedIn(1))
	_, _, err = svc.ReceiveSupplierReceipt(ctx, item.ID, filling.ID, 2)
	require.NoError(t, err)
	filled, _, err := svc.ListBackorders(ctx, service.ListBackordersInput{InventoryItemID: item.ID, Status: "filled"})
	require.NoError(t, err)
	require.Len(t, filled, 1, "the receipt filled the claim, which still owes its two units on paper")
	now, err := svc.AvailableQuantitiesByLocation(ctx, []string{item.ID})
	require.NoError(t, err)
	require.LessOrEqual(t, now[item.ID][loc.ID], int64(0), "and the warehouse has nothing to sell")

	newReceipt(ctx, t, svc, item.ID, loc.ID, 5, expectedIn(-24))
	canceled := newReceipt(ctx, t, svc, item.ID, loc.ID, 5, expectedIn(12))
	_, err = svc.CancelSupplierReceipt(ctx, item.ID, canceled.ID)
	require.NoError(t, err)
	due := newReceipt(ctx, t, svc, item.ID, loc.ID, 2, expectedIn(24))

	forecast, err := svc.RestockForecast(ctx, []string{item.ID})
	require.NoError(t, err)
	require.Contains(t, forecast, item.ID, "the receipt still expected leaves units, which no waiting claim takes")
	assert.Len(t, forecast[item.ID], 1)
	assert.True(t, due.ExpectedAt.Equal(forecast[item.ID][loc.ID]),
		"forecast %v, the receipt still due %v", forecast[item.ID][loc.ID], due.ExpectedAt)
}

// TestTheReverseMigrationKeepsTheArithmetic is I6: on a database of its own,
// a supplier_receipt movement survives the reverse of 000009 as an adjustment
// naming nothing, the table goes, and the migration applies again.
func TestTheReverseMigrationKeepsTheArithmetic(t *testing.T) {
	ctx := context.Background()
	src := inventory.New().Migrations()
	dsn := testdb.New(t, testDSN, "inventory_receipt_down")
	require.NoError(t, db.Migrate(ctx, dsn, src, inventory.ModuleName))

	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, `INSERT INTO stock_locations (id, name) VALUES ('sloc_down', 'Down')`)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `INSERT INTO inventory_items (id, sku) VALUES ('invitem_down', 'SKU-DOWN')`)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `INSERT INTO inventory_movements
	    (id, inventory_item_id, location_id, reason, delta, stocked_after, reference)
	    VALUES ('invmov_down', 'invitem_down', 'sloc_down', 'supplier_receipt', 4, 4, 'invsup_down')`)
	require.NoError(t, err)

	require.NoError(t, db.MigrateDown(ctx, dsn, src, inventory.ModuleName, 1))

	var reason string
	var reference *string
	var delta int64
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT reason, reference, delta FROM inventory_movements WHERE id = 'invmov_down'`).
		Scan(&reason, &reference, &delta))
	assert.Equal(t, "adjustment", reason, "the arithmetic is still true; the fact is what is lost")
	assert.Nil(t, reference, "an adjustment names nothing")
	assert.Equal(t, int64(4), delta)
	assert.False(t, testdb.TableExists(t, dsn, "inventory_supplier_receipts"))

	require.NoError(t, db.Migrate(ctx, dsn, src, inventory.ModuleName))
	assert.True(t, testdb.TableExists(t, dsn, "inventory_supplier_receipts"))
}

// TestTheCountsReadWhatIsExpectedWhereTheyAsk is I7 on real SQL: an expected
// receipt keeps its warehouse open and its item; a canceled one keeps neither,
// and one at another warehouse does not keep this one open.
func TestTheCountsReadWhatIsExpectedWhereTheyAsk(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, here, there := addItem(ctx, t, svc), addLocation(ctx, t, svc), addLocation(ctx, t, svc)

	held := newReceipt(ctx, t, svc, item.ID, here.ID, 2, expectedIn(24))
	_, err := svc.CloseStockLocation(ctx, here.ID)
	assert.Equal(t, service.CodeLocationNotEmpty, errors.CodeOf(err), "%v", err)
	err = svc.DeleteInventoryItem(ctx, item.ID)
	assert.Equal(t, service.CodeItemExpectsUnits, errors.CodeOf(err), "%v", err)

	_, err = svc.CancelSupplierReceipt(ctx, item.ID, held.ID)
	require.NoError(t, err)
	elsewhere := addItem(ctx, t, svc)
	newReceipt(ctx, t, svc, elsewhere.ID, there.ID, 2, expectedIn(24))

	_, err = svc.CloseStockLocation(ctx, here.ID)
	require.NoError(t, err, "a canceled receipt, and one owed to another warehouse, keep nothing open")
	require.NoError(t, svc.DeleteInventoryItem(ctx, item.ID), "a canceled receipt keeps no item")
}

// TestAReceiptCopiesItsMovement is I8: the received count and moment are the
// supplier_receipt movement's own, read off the same row.
func TestAReceiptCopiesItsMovement(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := withStock(ctx, t, svc, 2)
	receipt := newReceipt(ctx, t, svc, item.ID, loc.ID, 5, expectedIn(24))

	got, level, err := svc.ReceiveSupplierReceipt(ctx, item.ID, receipt.ID, 4)
	require.NoError(t, err)
	require.NotNil(t, level)
	assert.Equal(t, int64(6), level.StockedQuantity)

	var delta int64
	var at time.Time
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT delta, created_at FROM inventory_movements WHERE reason = 'supplier_receipt' AND reference = $1`,
		receipt.ID).Scan(&delta, &at))
	assert.Equal(t, delta, got.ReceivedQuantity)
	require.NotNil(t, got.ReceivedAt)
	assert.True(t, at.Equal(*got.ReceivedAt), "the receipt's moment %v is the movement's %v", *got.ReceivedAt, at)
	assert.True(t, at.Equal(got.UpdatedAt))
}

// TestTheForecastIsWhatTheReceiptsDo is P1 (ADR 0249): shelves, waiting claims
// and receipts at two warehouses are drawn, the forecast is read, and then the
// receipts are received through the service in the order they are expected. A
// warehouse that had nothing to sell first sells after the receipt the forecast
// named, and one it named nothing for never does. Receipt quantities are drawn
// below, at and above a claim's units in equal measure, which is where a fill
// passes a claim over, completes it exactly, or leaves units on sale.
func TestTheForecastIsWhatTheReceiptsDo(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	locations := []string{addLocation(ctx, t, svc).ID, addLocation(ctx, t, svc).ID}

	rapid.Check(t, func(rt *rapid.T) {
		item, err := svc.CreateInventoryItem(ctx, service.CreateInventoryItemInput{
			SKU: "SKU-" + models.NewInventoryItemID(), Title: "forecast",
		})
		require.NoError(rt, err)

		for i, location := range locations {
			if rapid.Bool().Draw(rt, "leveled") {
				_, err := svc.SetInventoryLevel(ctx, item.ID, location,
					rapid.Int64Range(0, 2).Draw(rt, "shelf"+string(rune('0'+i))))
				require.NoError(rt, err)
			}
		}

		var owed []int64
		for i := range rapid.IntRange(0, 3).Draw(rt, "claims") {
			quantity := rapid.Int64Range(1, 4).Draw(rt, "owed")
			names := rapid.SampledFrom([][]string{{locations[0]}, {locations[1]}, locations}).Draw(rt, "ranked")
			_, err := svc.ClaimBackorder(ctx, service.ClaimBackorderInput{
				InventoryItemID: item.ID, OrderID: testSaleOrderID,
				OrderLineItemID: "oli_forecast_" + models.NewBackorderID() + string(rune('a'+i)),
				Quantity:        quantity, LocationIDs: names,
			})
			require.NoError(rt, err)
			owed = append(owed, quantity)
		}

		base := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
		var receipts []models.SupplierReceipt
		for range rapid.IntRange(1, 4).Draw(rt, "receipts") {
			against := int64(2)
			if len(owed) > 0 {
				against = rapid.SampledFrom(owed).Draw(rt, "against")
			}
			quantity := against
			switch rapid.SampledFrom([]string{"below", "at", "above"}).Draw(rt, "class") {
			case "below":
				quantity = max(1, against-1)
			case "above":
				quantity = against + 1
			}
			at := base.Add(time.Duration(rapid.IntRange(0, 2).Draw(rt, "hour")) * time.Hour)
			location := rapid.SampledFrom(locations).Draw(rt, "at")
			receipts = append(receipts, newReceipt(ctx, rt, svc, item.ID, location, quantity, at))
		}

		forecast, err := svc.RestockForecast(ctx, []string{item.ID})
		require.NoError(rt, err)
		now, err := svc.AvailableQuantitiesByLocation(ctx, []string{item.ID})
		require.NoError(rt, err)

		slices.SortFunc(receipts, func(a, b models.SupplierReceipt) int {
			if c := a.ExpectedAt.Compare(b.ExpectedAt); c != 0 {
				return c
			}
			return cmp.Compare(a.ID, b.ID)
		})
		happened := map[string]time.Time{}
		for _, receipt := range receipts {
			_, level, err := svc.ReceiveSupplierReceipt(ctx, item.ID, receipt.ID, receipt.Quantity)
			require.NoError(rt, err)
			_, dated := happened[receipt.LocationID]
			if !dated && now[item.ID][receipt.LocationID] <= 0 && level.Available() > 0 {
				happened[receipt.LocationID] = receipt.ExpectedAt
			}
		}

		want := forecast[item.ID]
		if want == nil {
			want = map[string]time.Time{}
		}
		require.Equal(rt, len(want), len(happened), "forecast %v, receiving %v", want, happened)
		for location, at := range happened {
			require.True(rt, at.Equal(want[location]), "%s: forecast %v, receiving %v", location, want[location], at)
		}
	})
}
