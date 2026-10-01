package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestTheOrdersAwaitingTheirPaymentAreListed is ADR 0294's rule: an order
// awaits its payment when it is not canceled and what was collected falls
// short of its total less its credits. A partly paid order awaits the rest; an
// order paid and then refunded does not, because refunds are not added back;
// a credit lowers what is awaited.
func TestTheOrdersAwaitingTheirPaymentAreListed(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	place := func() string {
		t.Helper()

		order, err := e.svc.CreateOrder(ctx, validInput())
		require.NoError(t, err)
		return order.ID
	}
	collected := func(orderID string, paid, refunded int64) {
		t.Helper()

		_, err := e.svc.SetOrderSummaryTotals(ctx, orderID, service.SummaryTotalsInput{
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
	collected(credited, total-1000, 0)
	_, err := e.svc.CreateCreditLine(ctx, credited, service.CreateCreditLineInput{Amount: 1000, Reason: "goodwill"})
	require.NoError(t, err)
	canceled := place()
	require.NoError(t, e.svc.CancelOrder(ctx, canceled, "never paid"))

	listed := func(awaiting bool) []string {
		t.Helper()

		page, err := e.svc.ListOrders(ctx, service.ListOrdersInput{
			AwaitingPayment: &awaiting, Page: service.Page{Limit: 50},
		})
		require.NoError(t, err)
		ids := make([]string, 0, len(page.Items))
		for i := range page.Items {
			ids = append(ids, page.Items[i].ID)
		}
		return ids
	}

	assert.ElementsMatch(t, []string{unpaid, partly}, listed(true))
	assert.ElementsMatch(t, []string{settled, refunded, credited, canceled}, listed(false))
}

// TestTheProviderFiltersTheOrdersAwaitingTheirPayment: the read layer's
// awaiting_payment filter is the listing's, and it takes a bool only.
func TestTheProviderFiltersTheOrdersAwaitingTheirPayment(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	unpaid, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	paid, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	_, err = e.svc.SetOrderSummaryTotals(ctx, paid.ID, service.SummaryTotalsInput{PaidTotal: paid.Total})
	require.NoError(t, err)
	provider := service.NewQueryProvider(e.svc)

	records, err := provider.List(ctx, query.ListOptions{
		Fields: []string{query.IDField}, Filters: map[string]any{service.FilterAwaitingPayment: true},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, unpaid.ID, records[0][query.IDField])

	_, err = provider.List(ctx, query.ListOptions{Filters: map[string]any{service.FilterAwaitingPayment: "yes"}})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
}
