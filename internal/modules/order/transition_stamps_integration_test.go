//go:build integration

// The tests here run against a real PostgreSQL (and therefore Docker); to run
// them: make test-integration
//
// They exist because the change they cover IS SQL. The unit tests prove the
// service's decisions against a fake store, and a fake store cannot disagree
// with a CHECK constraint it does not have: it would happily hold an exchange
// that is canceled with no moment, or an order carrying an archive stamp it
// never entered. What is proved here is that the schema refuses both.
package order_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestArchivingStampsTheMomentOnTheRealDatabase is D5 closed against the
// schema.
//
// Archiving used to write nothing but updated_at, so the day an order left the
// daily lists was recoverable from no column. The three assertions are the
// whole claim: the moment exists, it is the DATABASE's (the query's now(), in
// UTC, not the process's clock), and completed_at did not move with it.
func TestArchivingStampsTheMomentOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	completed, err := svc.CompleteOrder(ctx, ord.ID)
	require.NoError(t, err)
	require.Nil(t, completed.ArchivedAt, "completing is not archiving")

	archived, err := svc.ArchiveOrder(ctx, ord.ID)
	require.NoError(t, err)

	require.NotNil(t, archived.ArchivedAt)
	assert.Equal(t, "UTC", archived.ArchivedAt.Location().String())
	require.NotNil(t, archived.CompletedAt)
	assert.Equal(t, *completed.CompletedAt, *archived.CompletedAt,
		"archiving must not move the moment the order was completed")

	// Read the column back through a second path. The RETURNING clause could
	// report a value the row does not keep, and the point of the column is that
	// it is still there on the next read.
	var stamped bool
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT archived_at IS NOT NULL FROM orders WHERE id = $1`, ord.ID).Scan(&stamped))
	assert.True(t, stamped, "the moment has to be ON THE ROW, not only in the response")
}

// TestTheDatabaseRefusesAnArchiveStampOnALiveOrder is the direction of the
// constraint that CAN be enforced.
//
// The mirror form the sibling stamps use — status and stamp implying each other
// — was not available: orders archived before the column existed carry the
// status and no moment, and adding it would have meant inventing one for them.
// This direction holds for every row that has ever existed, and it catches the
// fault worth catching.
func TestTheDatabaseRefusesAnArchiveStampOnALiveOrder(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE orders SET archived_at = now() WHERE id = $1`, ord.ID)

	require.Error(t, err, "a pending order may not carry an archive stamp")
	assert.Contains(t, err.Error(), "orders_archived_stamp")
}

// TestAnArchivedOrderMayHaveNoMoment is the honest half, and it is tested
// because the absence is a DECISION rather than an oversight.
//
// A row archived before migration 000007 has the status and no stamp. If the
// constraint had been written in the mirror form, this row could not exist —
// and the only way to make it exist would have been to backfill a moment nobody
// recorded. The write below is what such a row looks like, and the schema has
// to accept it.
func TestAnArchivedOrderMayHaveNoMoment(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	_, err = svc.CompleteOrder(ctx, ord.ID)
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE orders SET status = 'archived' WHERE id = $1`, ord.ID)
	require.NoError(t, err, "this is the shape of a row archived before the column existed")

	detail, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderArchived, detail.Status)
	assert.Nil(t, detail.ArchivedAt, "the moment is missing, and that is the truth about it")
}

// TestAnExchangeCanBeWithdrawnOnTheRealDatabase is D4's write half.
//
// order_exchanges had no UPDATE at all: every record was born "requested" and
// stayed there, and canceled_at was NULL on every row that has ever existed.
// This is the first transition the table has.
func TestAnExchangeCanBeWithdrawnOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	exchange, err := svc.CreateExchange(ctx, service.CreateExchangeInput{
		OrderID: ord.ID, DifferenceDue: -500, Note: "the size did not fit",
	})
	require.NoError(t, err)
	require.Equal(t, models.ExchangeRequested, exchange.Status)
	require.Nil(t, exchange.CanceledAt)

	canceled, err := svc.CancelExchange(ctx, exchange.ID)
	require.NoError(t, err)

	assert.Equal(t, models.ExchangeCanceled, canceled.Status)
	require.NotNil(t, canceled.CanceledAt)
	assert.Equal(t, "UTC", canceled.CanceledAt.Location().String())

	// A second call keeps the FIRST moment. Against the real database this also
	// exercises the row lock: the read of the status and the decision not to
	// write happen inside one transaction.
	second, err := svc.CancelExchange(ctx, exchange.ID)
	require.NoError(t, err, "a second withdrawal is a no-op, not a conflict")
	require.NotNil(t, second.CanceledAt)
	assert.Equal(t, *canceled.CanceledAt, *second.CanceledAt)

	// And the row itself, read back outside the service.
	var status string
	var stamped bool
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT status, canceled_at IS NOT NULL FROM order_exchanges WHERE id = $1`,
		exchange.ID).Scan(&status, &stamped))
	assert.Equal(t, "canceled", status)
	assert.True(t, stamped)
}

// TestTheExchangeTableRefusesACompletion is D4's other half, and it is the
// point of the whole decision.
//
// Completing an exchange needs goods shipped out against an existing order and,
// when the difference is positive, money collected against one. The framework
// has no capability for the first — settling a claim with a replacement is
// refused for exactly that reason — and the order-to-payment link is one-to-one,
// which forbids the second. Rather than keep a status and a stamp for a
// transition nothing could perform, the schema now refuses the state.
func TestTheExchangeTableRefusesACompletion(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	exchange, err := svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: ord.ID})
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE order_exchanges SET status = 'completed' WHERE id = $1`, exchange.ID)

	require.Error(t, err, "a state nothing can reach must not be writable")
	assert.Contains(t, err.Error(), "order_exchanges_status_valid")

	// The column is gone too, not merely unused. A column that still existed
	// would keep coming back on every SELECT * and keep looking like a field
	// waiting to be filled.
	var present bool
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'order_exchanges' AND column_name = 'completed_at'
		)`).Scan(&present))
	assert.False(t, present, "order_exchanges.completed_at must be gone")
}

// TestAWithdrawnExchangeCannotLoseItsMoment is the mirror constraint the order
// could not have.
//
// It is addable HERE precisely because the column was dead: no row could hold
// "canceled", so every existing row satisfies both directions of the rule. The
// dead column is what bought the strong constraint.
func TestAWithdrawnExchangeCannotLoseItsMoment(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	exchange, err := svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: ord.ID})
	require.NoError(t, err)

	// A status without its moment.
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE order_exchanges SET status = 'canceled' WHERE id = $1`, exchange.ID)
	require.Error(t, err, "a withdrawn exchange without a moment is the state D4 described")
	assert.Contains(t, err.Error(), "order_exchanges_canceled_stamp")

	// And the reverse: a moment without its status.
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE order_exchanges SET canceled_at = now() WHERE id = $1`, exchange.ID)
	require.Error(t, err, "a moment on an open request would date a withdrawal that did not happen")
	assert.Contains(t, err.Error(), "order_exchanges_canceled_stamp")
}

// TestWithdrawingAnExchangeTwiceUnderTheLockIsSafe is the concurrency claim,
// and it can only be made here.
//
// The fake store's lock is a recording of an intention; this one is FOR UPDATE
// on a real row. Two withdrawals racing must produce one moment, not the later
// of two: the second transaction blocks on the lock, then reads "canceled" and
// takes the no-op branch.
func TestWithdrawingAnExchangeTwiceUnderTheLockIsSafe(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	exchange, err := svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: ord.ID})
	require.NoError(t, err)

	const racers = 8

	type outcome struct {
		record models.Exchange
		err    error
	}
	results := make(chan outcome, racers)
	start := make(chan struct{})

	for range racers {
		go func() {
			<-start
			record, cancelErr := svc.CancelExchange(context.Background(), exchange.ID)
			results <- outcome{record: record, err: cancelErr}
		}()
	}
	close(start)

	var moments []string
	for range racers {
		got := <-results
		require.NoError(t, got.err, "every racer either writes the moment or reads it")
		require.NotNil(t, got.record.CanceledAt)
		moments = append(moments, got.record.CanceledAt.String())
	}

	for i := range moments {
		assert.Equal(t, moments[0], moments[i],
			"all %d callers must see ONE moment; a differing one means a re-stamp got through",
			racers)
	}
}

// TestAnExchangeOnACanceledOrderCanStillBeWithdrawn is the asymmetry with
// opening one, checked where the order's own state is real.
//
// CreateExchange requires a live order. CancelExchange deliberately does not:
// refusing to close a request because the order moved would leave the record
// open with no way to close it, and the record would then say an exchange is
// pending forever.
func TestAnExchangeOnACanceledOrderCanStillBeWithdrawn(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	exchange, err := svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: ord.ID})
	require.NoError(t, err)

	require.NoError(t, svc.CancelOrder(ctx, ord.ID, "out of stock"))

	// Opening a new one is refused; closing the existing one is not.
	_, err = svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: ord.ID})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "got: %v", err)

	canceled, err := svc.CancelExchange(ctx, exchange.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ExchangeCanceled, canceled.Status)
}
