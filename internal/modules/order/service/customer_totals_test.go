package service_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestCustomerOrderTotalsAreReadForABoundedPage: a call names at least one and
// at most MaxCustomerTotals customers, none blank.
func TestCustomerOrderTotalsAreReadForABoundedPage(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	ctx := context.Background()
	many := make([]string, service.MaxCustomerTotals+1)
	for i := range many {
		many[i] = "cus_x"
	}

	for name, ids := range map[string][]string{"none": nil, "too many": many, "blank": {"cus_a", " "}} {
		_, err := e.svc.CustomerOrderTotals(ctx, ids, nil)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), name)
	}
	_, err := e.svc.CustomerOrderTotals(ctx, many[:service.MaxCustomerTotals], nil)
	require.NoError(t, err, "a full page is read")
}

// TestTheInteropCarriesTheTotalsPerCurrency: the JSON the segment flow reads
// names each customer's currency, count and net spend.
func TestTheInteropCarriesTheTotalsPerCurrency(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	seed := func(id, customer, currency string, total int64, status models.OrderStatus, at time.Time) {
		e.store.seedOrder(models.Order{
			ID: id, Status: status, RegionID: testRegionID, CustomerID: customer,
			CurrencyCode: currency, Subtotal: total, Total: total, PlacedAt: at,
		})
	}
	seed("order_1", "cus_a", "TRY", 5_000, models.OrderPending, since)
	seed("order_2", "cus_a", "TRY", 3_000, models.OrderCompleted, since.Add(time.Hour))
	seed("order_3", "cus_a", "EUR", 700, models.OrderCompleted, since.Add(time.Hour))
	seed("order_4", "cus_a", "TRY", 9_000, models.OrderCanceled, since.Add(time.Hour))
	seed("order_5", "cus_a", "TRY", 9_000, models.OrderCompleted, since.Add(-time.Second))
	seed("order_6", "cus_b", "TRY", 1_000, models.OrderCompleted, since.Add(time.Hour))
	e.store.seedRefund("order_2", 1_000)

	raw, err := service.NewInterop(e.svc).CustomerOrderTotalsJSON(context.Background(), []string{"cus_a", "cus_c"}, &since)

	require.NoError(t, err)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(raw, &rows))
	assert.Equal(t, []map[string]any{
		{"customer_id": "cus_a", "currency_code": "EUR", "orders": float64(1), "net_spend": float64(700)},
		{"customer_id": "cus_a", "currency_code": "TRY", "orders": float64(2), "net_spend": float64(7_000)},
	}, rows, "canceled, earlier and other customers' orders left out; the refund deducted")
}
