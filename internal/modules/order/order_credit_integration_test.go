//go:build integration

// The tests here run against a real PostgreSQL; to run them:
// make test-integration
//
// What they cover is the half a fake cannot: the ceiling holding when several
// credits are written AT THE SAME TIME. The service reads a sum and then writes
// a row, and under READ COMMITTED two concurrent credits would each read the sum
// before the other committed. Only a real database can show that the order's
// lock is what stops it. And the table's own refusal of a credit that is not
// positive, whatever wrote the row (ADR 0105, ADR 0394).
package order_test

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/repository"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestConcurrentCreditsCannotPassTheCeiling is the race the order's lock exists
// for here.
//
// Ten goroutines each try to write off 1000 against an order worth 6100. Six fit
// and four cannot, and without the lock every one of them reads a credited total
// from before the others committed — all ten pass the check and the order is
// written off nearly twice over.
//
// It runs several ROUNDS because a race that loses once in three is a race that
// passes a single-round test whenever it feels like it (ADR 0091 measured
// exactly that shape).
func TestConcurrentCreditsCannotPassTheCeiling(t *testing.T) {
	const (
		rounds  = 5
		writers = 10
		each    = 1000
	)

	ctx := context.Background()
	svc, _ := newService(t)

	for round := range rounds {
		order, err := svc.CreateOrder(ctx, validInput())
		require.NoError(t, err, "round %d", round)

		var start, finish sync.WaitGroup
		errs := make([]error, writers)

		start.Add(1)
		finish.Add(writers)

		for i := range writers {
			go func(idx int) {
				defer finish.Done()
				start.Wait()

				_, errs[idx] = svc.CreateCreditLine(ctx, order.ID, service.CreateCreditLineInput{
					Amount: each, Reason: "concurrent",
				})
			}(i)
		}

		start.Done()
		finish.Wait()

		granted := 0
		for i := range errs {
			if errs[i] == nil {
				granted++
			}
		}

		detail, err := svc.GetOrder(ctx, order.ID)
		require.NoError(t, err)

		assert.Equal(t, int64(granted*each), detail.CreditedTotal,
			"round %d: every credit that reported success has to be in the total", round)
		assert.LessOrEqual(t, detail.CreditedTotal, order.Total,
			"round %d: the credited total went past the order's total, which is the "+
				"read-then-write race the order's lock exists to stop", round)
		assert.Positive(t, granted, "round %d: at least one writer has to win", round)
	}
}

// TestTheCreditTableRefusesACharge is ADR 0394 in the schema: a credit row of
// zero or less is refused by order_credit_lines itself, so a writer that goes
// past the service's guard still cannot withdraw a credit or charge a sold
// line through it.
//
// The rows go in as raw SQL, past the service, on a database of their own:
// the guard in front of the table is the unit test's, and this is the table.
func TestTheCreditTableRefusesACharge(t *testing.T) {
	ctx := context.Background()
	_, pool := isolatedDatabase(ctx, t, "order_credit_charge")
	svc, _ := newServiceWithStore(t, repository.New(pool.Pool()))

	placed, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	insert := func(id string, amount int64) error {
		_, err := pool.Pool().Exec(ctx,
			`INSERT INTO order_credit_lines (id, order_id, amount, reason) VALUES ($1, $2, $3, 'test')`,
			id, placed.ID, amount)

		return err
	}

	for id, amount := range map[string]int64{"ocl_charge": -1, "ocl_zero": 0} {
		var pgErr *pgconn.PgError
		require.ErrorAs(t, insert(id, amount), &pgErr, "amount %d", amount)
		assert.Equal(t, "order_credit_lines_amount_positive", pgErr.ConstraintName, "amount %d", amount)
	}

	// The control: the smallest credit there is goes in, so the refusals above
	// are the amount's and not the row's.
	require.NoError(t, insert("ocl_one", 1))
}
