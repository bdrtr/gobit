//go:build integration

// The tests in this file prove the GROUND under the spending limit: that the SQL
// computing the total really does filter the window, that it does not count a
// canceled order, that it deducts the refund and — most importantly — that the
// advisory lock really does stop two concurrent orders from exceeding the limit
// TOGETHER.
//
// The unit tests (service/spending_test.go) prove the service's DECISIONS with a
// fake store; the fake imitates the rules of the total. That the imitation
// matches reality and that the lock really serializes can only be seen here, on
// a real PostgreSQL and with real goroutines.
package order_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// fixedRule is the provider that returns the same spending rule on every call.
//
// The real provider is the b2b module and this package CANNOT import it
// (Principle 2.4); the only thing imitated here is the limit's JSON SCHEMA. That
// the schema is the same on both sides cannot be proved by these tests — for its
// counterpart on the b2b side see
// internal/modules/b2b/service/interop_test.go.
type fixedRule struct {
	payload json.RawMessage
}

// SpendingLimitJSON returns the rule it was set up with.
func (k fixedRule) SpendingLimitJSON(context.Context, string) (json.RawMessage, error) {
	return k.payload, nil
}

// limitRule produces a rule body with the given limit and window.
func limitRule(t *testing.T, limit int64, window *time.Time) json.RawMessage {
	t.Helper()

	body := map[string]any{
		"limited":        true,
		"spending_limit": limit,
		"currency_code":  testCurrency,
		"window_start":   "",
	}
	if window != nil {
		body["window_start"] = window.UTC().Format(time.RFC3339)
	}
	payload, err := json.Marshal(body)
	require.NoError(t, err)
	return payload
}

// limitedService sets up a service with the spending rule attached, running on a
// real store.
func limitedService(t *testing.T, rule json.RawMessage) *service.Service {
	t.Helper()

	bus := eventbus.NewInMemory(nil)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := bus.Shutdown(shutdownCtx); err != nil {
			t.Logf("the event bus could not be shut down: %v", err)
		}
	})

	svc, err := service.New(service.Options{
		Repo:     repository.New(testPool.Pool()),
		Events:   bus,
		Spending: fixedRule{payload: rule},
	})
	require.NoError(t, err)
	return svc
}

// writePastOrder writes an order to the store DIRECTLY and pins its placed_at to
// the moment the caller gives.
//
// Writing it over the service is not possible: placed_at is stamped by the
// database with now(), and an order that stays OUTSIDE the window cannot be set
// up any other way. The rule being exercised is still the service's rule; what is
// set up here is only the PAST.
func writePastOrder(ctx context.Context, t *testing.T, customerID string, total int64, placedAt time.Time) string {
	t.Helper()

	id := models.NewOrderID()
	_, err := testPool.Pool().Exec(ctx, `
        INSERT INTO orders (
            id, status, region_id, customer_id, currency_code,
            subtotal, discount_total, tax_total, shipping_total, total, placed_at
        ) VALUES ($1, 'pending', $2, $3, $4, $5, 0, 0, 0, $5, $6)`,
		id, testRegionID, customerID, testCurrency, total, placedAt.UTC())
	require.NoError(t, err)
	return id
}

// limitedInput produces an order input of 6100 for the given customer.
func limitedInput(customerID string) service.CreateOrderInput {
	input := validInput()
	input.CustomerID = customerID
	return input
}

// TestSpendingOutsideTheWindowIsReallyNotCounted verifies that the period is
// enforced in SQL.
//
// An order of 50000 placed a day before the window must not block an employee
// whose limit is 10000; the 5000 INSIDE the window, on the other hand, pushes
// past the limit together with the new order of 6100. The two claims stand in the
// same test because they are two branches of the same query: exercising only one
// of them would also let through the case where the filter is never applied at
// all.
func TestSpendingOutsideTheWindowIsReallyNotCounted(t *testing.T) {
	ctx := context.Background()
	customer := "cus_WINDOW"
	window := time.Now().UTC().Truncate(time.Hour)

	writePastOrder(ctx, t, customer, 50_000, window.Add(-24*time.Hour))
	svc := limitedService(t, limitRule(t, 10_000, &window))

	// Because the 50000 outside the window is not counted, the order GOES
	// THROUGH.
	_, err := svc.CreateOrder(ctx, limitedInput(customer))
	require.NoError(t, err, "the previous period's spending must not burn this period's budget")

	// Now 6100 has been spent INSIDE the window; the second order pushes past the
	// limit.
	_, err = svc.CreateOrder(ctx, limitedInput(customer))
	require.Error(t, err)
	assert.Equal(t, service.CodeSpendingLimitExceeded, errors.CodeOf(err))
}

// TestWithoutAWindowTheWholeHistoryIsCounted verifies the SQL counterpart of the
// "never" period.
//
// When window_start arrives empty the filter is not applied at all and an order
// from years ago enters the total as well.
func TestWithoutAWindowTheWholeHistoryIsCounted(t *testing.T) {
	ctx := context.Background()
	customer := "cus_NOWINDOW"

	writePastOrder(ctx, t, customer, 5000, time.Date(2019, time.March, 3, 0, 0, 0, 0, time.UTC))
	svc := limitedService(t, limitRule(t, 10_000, nil))

	_, err := svc.CreateOrder(ctx, limitedInput(customer))
	require.Error(t, err)
	assert.Equal(t, service.CodeSpendingLimitExceeded, errors.CodeOf(err))
}

// TestCanceledOrderDoesNotEnterSpending verifies that a cancellation REALLY does
// give the budget back.
//
// The order is canceled over the service (status = 'canceled'), that is, what is
// exercised is the query's status filter. Had a canceled order held on to the
// budget, every attempt whose payment was declined would permanently burn the
// employee's period allowance — and that is exactly how the saga cancels the
// order after a failed attempt.
func TestCanceledOrderDoesNotEnterSpending(t *testing.T) {
	ctx := context.Background()
	customer := "cus_CANCEL"
	window := time.Now().UTC().Add(-time.Hour)
	svc := limitedService(t, limitRule(t, 10_000, &window))

	first, err := svc.CreateOrder(ctx, limitedInput(customer))
	require.NoError(t, err)

	// BEFORE the cancellation the second order exceeds the limit.
	_, err = svc.CreateOrder(ctx, limitedInput(customer))
	require.Error(t, err)

	require.NoError(t, svc.CancelOrder(ctx, first.ID, "the payment was declined"))

	_, err = svc.CreateOrder(ctx, limitedInput(customer))
	assert.NoError(t, err, "a canceled order must release the budget")
}

// TestRefundedAmountIsReallyDeducted verifies that the refund comes back to the
// budget.
//
// The query's LEFT JOIN can only be exercised here, with a real order_summaries
// row: on an order without a summary the refund must count as zero, whereas on an
// order that has one it must be deducted.
func TestRefundedAmountIsReallyDeducted(t *testing.T) {
	ctx := context.Background()
	customer := "cus_REFUND"
	window := time.Now().UTC().Add(-time.Hour)
	svc := limitedService(t, limitRule(t, 10_000, &window))

	first, err := svc.CreateOrder(ctx, limitedInput(customer))
	require.NoError(t, err)

	_, err = svc.SetOrderSummaryTotals(ctx, first.ID, service.SummaryTotalsInput{
		PaidTotal:     6100,
		RefundedTotal: 6100,
	})
	require.NoError(t, err)

	_, err = svc.CreateOrder(ctx, limitedInput(customer))
	assert.NoError(t, err, "a fully refunded order must not hold on to the budget")
}

// TestConcurrentOrdersCannotExceedTheLimitTogether verifies that the RACE
// between the check and the write is closed.
//
// The limit is enough for exactly ONE order (6100 <= 10000 < 12200). Eight
// goroutines try to open an order at the same time; without the lock they would
// all read the total as 0, all see themselves below the limit and all be written
// — the classic write skew. Under the lock only ONE must go through, and the rest
// must fall with a limit overrun.
//
// This claim cannot be set up with a fake store: the thing that serializes is
// PostgreSQL's pg_advisory_xact_lock, and it can only be seen with real
// transactions.
func TestConcurrentOrdersCannotExceedTheLimitTogether(t *testing.T) {
	ctx := context.Background()
	customer := "cus_RACE"
	window := time.Now().UTC().Add(-time.Hour)
	svc := limitedService(t, limitRule(t, 10_000, &window))

	const attempts = 8
	var succeeded, exceeded atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})

	wg.Add(attempts)
	for range attempts {
		go func() {
			defer wg.Done()
			<-start

			_, err := svc.CreateOrder(ctx, limitedInput(customer))
			switch {
			case err == nil:
				succeeded.Add(1)
			case errors.CodeOf(err) == service.CodeSpendingLimitExceeded:
				exceeded.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.Equal(t, int64(1), succeeded.Load(), "the limit must be enough for only ONE order")
	assert.Equal(t, int64(attempts-1), exceeded.Load(), "the rest must fall with a limit overrun")

	// The real total in the database must not exceed the limit either.
	var total int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT COALESCE(SUM(total), 0) FROM orders
         WHERE customer_id = $1 AND deleted_at IS NULL AND status <> 'canceled'`,
		customer).Scan(&total))
	assert.LessOrEqual(t, total, int64(10_000))
}

// TestSpendingLockDoesNotHoldUpDifferentCustomers verifies that the lock is
// taken PER CUSTOMER.
//
// Had the lock serialized all the orders, the rule would still be applied
// correctly but the order-opening path would have become single-lane. This is why
// the concurrent orders of two different customers must BOTH go through.
func TestSpendingLockDoesNotHoldUpDifferentCustomers(t *testing.T) {
	ctx := context.Background()
	window := time.Now().UTC().Add(-time.Hour)
	svc := limitedService(t, limitRule(t, 10_000, &window))

	var wg sync.WaitGroup
	errs := make([]error, 2)
	customers := []string{"cus_SEPARATE_A", "cus_SEPARATE_B"}

	wg.Add(len(customers))
	for i, customer := range customers {
		go func() {
			defer wg.Done()
			_, errs[i] = svc.CreateOrder(ctx, limitedInput(customer))
		}()
	}
	wg.Wait()

	assert.NoError(t, errs[0])
	assert.NoError(t, errs[1])
}

// TestCustomerWithoutALimitTakesNoLock verifies that there is no extra cost on an
// order that has no rule.
//
// With a "limited": false body neither is a lock taken nor is the total read; the
// proof is that even a history large enough never to fit the window does not
// block the order.
func TestCustomerWithoutALimitTakesNoLock(t *testing.T) {
	ctx := context.Background()
	customer := "cus_UNLIMITED"

	writePastOrder(ctx, t, customer, 1_000_000, time.Now().UTC())
	svc := limitedService(t, json.RawMessage(`{"limited":false}`))

	_, err := svc.CreateOrder(ctx, limitedInput(customer))
	assert.NoError(t, err)
}
