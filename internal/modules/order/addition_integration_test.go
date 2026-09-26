//go:build integration

package order_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
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
