//go:build integration

package order_test

import (
	"context"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus/outbox"
	"github.com/bdrtr/gobit/internal/modules/order"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// cancelBarrierStore holds a cancellation after it has LOCKED the order and
// before it writes the new status, so a test can act while the lock is held.
//
// The lock is the one the service's own CancelOrder takes through the
// repository; nothing here copies its SQL, so a change to that lock is what this
// test measures.
type cancelBarrierStore struct {
	*repository.Repository
	locked  chan struct{}
	release chan struct{}
}

// CancelOrder announces that the order is locked and waits to be released.
func (s *cancelBarrierStore) CancelOrder(ctx context.Context, id, reason string) (models.Order, error) {
	close(s.locked)
	<-s.release

	return s.Repository.CancelOrder(ctx, id, reason)
}

// singleConnection opens a pool of exactly one connection and returns it with
// the backend that connection runs on, so the test knows who holds a lock
// before the transaction that takes it begins.
func singleConnection(ctx context.Context, t *testing.T) (pool *db.Pool, backendPID int32) {
	t.Helper()

	cfg := db.DefaultConfig(testDSN)
	cfg.MaxConns, cfg.MinConns = 1, 1
	pool, err := db.New(ctx, cfg, nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	require.NoError(t, pool.Pool().QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&backendPID))

	return pool, backendPID
}

// lockWaiters counts the sessions WAITING on a lock the given backend holds.
//
// It returns its error rather than asserting it, because it is polled inside
// require.Eventually, whose condition runs on a goroutine of its own.
func lockWaiters(ctx context.Context, blockerPID int32) (int64, error) {
	var waiters int64
	err := testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM pg_stat_activity
         WHERE datname = current_database()
           AND wait_event_type = 'Lock'
           AND $1 = ANY(pg_blocking_pids(pid))`, blockerPID).Scan(&waiters)

	return waiters, err
}

// additionsOf counts the orders that add to parentID, straight from the table.
func additionsOf(ctx context.Context, t *testing.T, parentID string) int64 {
	t.Helper()

	var n int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM orders WHERE adds_to_order_id = $1`, parentID).Scan(&n))

	return n
}

// TestAnAdditionWaitsForACancellationAndIsRefused is the race the share lock
// exists for (ADR 0192).
//
// The cancellation holds the parent's row lock and has not written yet. The
// addition's write has to WAIT on it — which the test sees in pg_stat_activity
// rather than inferring from timing — and, once the cancellation commits, read
// the parent it left and refuse. Reading the parent without the lock reads
// 'pending', never waits, and writes an addition to a canceled order; the
// foreign key alone waits too late, after the status was read.
func TestAnAdditionWaitsForACancellationAndIsRefused(t *testing.T) {
	ctx := context.Background()
	plain, _ := newService(t)

	parent, err := plain.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	pool, cancelerPID := singleConnection(ctx, t)
	barrier := &cancelBarrierStore{
		Repository: repository.New(pool.Pool()),
		locked:     make(chan struct{}),
		release:    make(chan struct{}),
	}
	canceler, _ := newServiceWithStore(t, barrier)

	canceled := make(chan error, 1)
	go func() { canceled <- canceler.CancelOrder(ctx, parent.ID, "the customer called") }()
	select {
	case <-barrier.locked:
	case <-time.After(10 * time.Second):
		t.Fatal("the cancellation never locked the order")
	}

	added := make(chan error, 1)
	go func() {
		in := validInput()
		in.AddsToOrderID = parent.ID
		_, addErr := plain.CreateOrder(ctx, in)
		added <- addErr
	}()

	require.Eventually(t, func() bool {
		waiters, waitErr := lockWaiters(ctx, cancelerPID)
		return waitErr == nil && waiters == 1
	}, 10*time.Second, 20*time.Millisecond,
		"the addition has to WAIT on the lock the cancellation holds")

	close(barrier.release)
	require.NoError(t, <-canceled)

	err = <-added
	require.Error(t, err, "an addition to an order canceled while it waited has to be refused")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeAdditionParentNotPending, errors.CodeOf(err))
	assert.Zero(t, additionsOf(ctx, t, parent.ID), "the refused addition left no row")
}

// TestAnAdditionWrittenFirstOutlivesItsParentsCancellation is the other order
// of the same two events: the addition is an order of its own, and a later
// cancellation of the parent does not reach it.
func TestAnAdditionWrittenFirstOutlivesItsParentsCancellation(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	parent, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	in := validInput()
	in.AddsToOrderID = parent.ID
	addition, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	require.NoError(t, svc.CancelOrder(ctx, parent.ID, "the customer called"))

	read, err := svc.GetOrder(ctx, addition.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderPending, read.Status)
	assert.Equal(t, parent.ID, read.AddsToOrderID)
}

// TestTheAdditionConstraintsAreTheLastDefence reaches the table past the
// service: an order cannot add to itself, and cannot name an order that does
// not exist.
func TestTheAdditionConstraintsAreTheLastDefence(t *testing.T) {
	ctx := context.Background()

	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO orders (id, region_id, currency_code, adds_to_order_id)
         VALUES ('order_SELF', $1, $2, 'order_SELF')`, testRegionID, testCurrency)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "orders_adds_to_another")

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO orders (id, region_id, currency_code, adds_to_order_id)
         VALUES ('order_ORPHAN', $1, $2, 'order_NOWHERE')`, testRegionID, testCurrency)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "orders_adds_to_order_id_fkey")
}

// isolatedDatabase creates a database of the test's own, applies the module's
// and the outbox's migrations to it, and returns its address and a pool on it.
//
// A test that drops the schema has to run here: in the shared database it
// would rewind the rows of every test before it and depend on which ones had
// run (D135).
func isolatedDatabase(ctx context.Context, t *testing.T, prefix string) (string, *db.Pool) {
	t.Helper()

	name := prefix + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	_, err := testPool.Pool().Exec(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)

	parsed, err := url.Parse(testDSN)
	require.NoError(t, err)
	parsed.Path = "/" + name
	dsn := parsed.String()

	require.NoError(t, db.Migrate(ctx, dsn, order.New().Migrations(), order.ModuleName))
	require.NoError(t, db.Migrate(ctx, dsn, outbox.Migrations(), outbox.MigrationOwner))
	pool, err := db.New(ctx, db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return dsn, pool
}

// tableExistsIn reports whether the table exists in the pool's database.
func tableExistsIn(ctx context.Context, t *testing.T, pool *db.Pool, table string) bool {
	t.Helper()

	var exists bool
	require.NoError(t, pool.Pool().QueryRow(ctx,
		`SELECT EXISTS (
             SELECT 1 FROM pg_class c
             JOIN pg_namespace n ON n.oid = c.relnamespace
             WHERE c.relname = $1 AND c.relkind = 'r' AND n.nspname = current_schema()
         )`, table).Scan(&exists))

	return exists
}

// correctableOrder places an order that ships to a Turkish address.
func correctableOrder(ctx context.Context, t *testing.T, svc *service.Service) models.Order {
	t.Helper()

	in := validInput()
	in.Addresses = []models.OrderAddress{{
		Type: models.AddressShipping, FirstName: "Ada", Address1: "12 Wrong St",
		City: "Springfield", PostalCode: "62701", CountryCode: "TR",
	}}
	placed, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	return placed
}

// TestACorrectionKeepsWhatTheOrderHeld runs a correction on the real schema
// (ADR 0195): two rows, one current, and the index that allows the history
// still refuses a second current address.
func TestACorrectionKeepsWhatTheOrderHeld(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	placed := correctableOrder(ctx, t, svc)

	_, err := svc.CorrectShippingAddress(ctx, placed.ID, models.OrderAddress{
		FirstName: "Ada", Address1: "12 Right St", City: "Springfield", PostalCode: "62701",
	})
	require.NoError(t, err)

	var current, superseded int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE superseded_at IS NULL),
                count(*) FILTER (WHERE superseded_at IS NOT NULL)
         FROM order_addresses WHERE order_id = $1 AND address_type = 'shipping'`,
		placed.ID).Scan(&current, &superseded))
	assert.Equal(t, 1, current)
	assert.Equal(t, 1, superseded)

	read, err := svc.GetOrder(ctx, placed.ID)
	require.NoError(t, err)
	assert.Equal(t, "12 Right St", read.ShippingAddress.Address1)

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO order_addresses (id, order_id, address_type, address_1, country_code)
         VALUES ('oaddr_SECOND_CURRENT', $1, 'shipping', 'Elsewhere', 'TR')`, placed.ID)
	require.Error(t, err, "a second CURRENT shipping address is a parcel with two destinations")
	assert.Contains(t, err.Error(), "order_addresses_one_current_per_type")

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE order_addresses SET superseded_at = created_at - interval '1 second'
         WHERE order_id = $1 AND superseded_at IS NOT NULL`, placed.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "order_addresses_superseded_after_written")
}

// TestARollbackRefusesADatabaseHoldingACorrection holds 000023's down file to
// what it says: the history a correction made is not dropped by a rollback.
func TestARollbackRefusesADatabaseHoldingACorrection(t *testing.T) {
	ctx := context.Background()
	dsn, pool := isolatedDatabase(ctx, t, "order_correction_rollback")
	svc, _ := newServiceWithStore(t, repository.New(pool.Pool()))
	placed := correctableOrder(ctx, t, svc)
	_, err := svc.CorrectShippingAddress(ctx, placed.ID, models.OrderAddress{Address1: "12 Right St"})
	require.NoError(t, err)

	err = db.MigrateDown(ctx, dsn, order.New().Migrations(), order.ModuleName, 22)

	require.Error(t, err, "the rollback dropped the address the order was placed with")
	assert.Contains(t, err.Error(), "order_addresses_one_per_type")
}

// TestASecondCorrectionLeavesTheFirstDated closes only the current row: the
// address the order was placed with keeps the moment the first correction
// closed it, which is the moment the timeline shows for that correction.
func TestASecondCorrectionLeavesTheFirstDated(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	placed := correctableOrder(ctx, t, svc)

	_, err := svc.CorrectShippingAddress(ctx, placed.ID, models.OrderAddress{Address1: "12 Right St"})
	require.NoError(t, err)
	var first time.Time
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT superseded_at FROM order_addresses
         WHERE order_id = $1 AND address_1 = '12 Wrong St'`, placed.ID).Scan(&first))

	_, err = svc.CorrectShippingAddress(ctx, placed.ID, models.OrderAddress{Address1: "12 Right St, Flat 3"})
	require.NoError(t, err)

	var again time.Time
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT superseded_at FROM order_addresses
         WHERE order_id = $1 AND address_1 = '12 Wrong St'`, placed.ID).Scan(&again))
	assert.True(t, first.Equal(again),
		"the second correction re-dated the first: %s became %s", first, again)
}
