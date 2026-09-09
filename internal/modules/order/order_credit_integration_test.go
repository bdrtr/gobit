//go:build integration

// The tests here run against a real PostgreSQL; to run them:
// make test-integration
//
// What they cover is the half a fake cannot: the ceiling holding when several
// credits are written AT THE SAME TIME. The service reads a sum and then writes
// a row, and under READ COMMITTED two concurrent credits would each read the sum
// before the other committed. Only a real database can show that the order's
// lock is what stops it.
package order_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
