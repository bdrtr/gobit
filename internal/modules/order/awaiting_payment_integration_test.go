//go:build integration

package order_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestTheAwaitingFilterOnTheRealQuery is ADR 0294's predicate against a real
// PostgreSQL, the listing and its count together: the unit test runs a fake
// written to match the query. The orders are one customer's, because the
// database is shared.
func TestTheAwaitingFilterOnTheRealQuery(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	customer := fmt.Sprintf("cus_awaiting_%d", time.Now().UnixNano())

	place := func() string {
		t.Helper()

		in := validInput()
		in.CustomerID = customer
		order, err := svc.CreateOrder(ctx, in)
		require.NoError(t, err)
		return order.ID
	}
	collected := func(orderID string, paid, refunded int64) {
		t.Helper()

		_, err := svc.SetOrderSummaryTotals(ctx, orderID, service.SummaryTotalsInput{
			PaidTotal: paid, RefundedTotal: refunded,
		})
		require.NoError(t, err)
	}
	total := validInput().Total

	unpaid := place()
	partly := place()
	collected(partly, total/2, 0)
	settled := place()
	collected(settled, total, 0)
	refunded := place()
	collected(refunded, total, total)
	credited := place()
	collected(credited, total-1, 0)
	_, err := svc.CreateCreditLine(ctx, credited, service.CreateCreditLineInput{Amount: 1, Reason: "goodwill"})
	require.NoError(t, err)
	canceled := place()
	require.NoError(t, svc.CancelOrder(ctx, canceled, "never paid"))

	listed := func(awaiting bool) ([]string, int64) {
		t.Helper()

		page, err := svc.ListOrders(ctx, service.ListOrdersInput{
			CustomerID: &customer, AwaitingPayment: &awaiting, Page: service.Page{Limit: 50},
		})
		require.NoError(t, err)
		ids := make([]string, 0, len(page.Items))
		for i := range page.Items {
			ids = append(ids, page.Items[i].ID)
		}
		return ids, page.Count
	}

	awaiting, count := listed(true)
	assert.ElementsMatch(t, []string{unpaid, partly}, awaiting)
	assert.Equal(t, int64(2), count, "the count applies the same predicate")
	others, count := listed(false)
	assert.ElementsMatch(t, []string{settled, refunded, credited, canceled}, others)
	assert.Equal(t, int64(4), count)
}
