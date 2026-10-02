//go:build integration

package order_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestACustomerBoughtWhatTheirOrdersCarry is ADR 0372's question against a
// real PostgreSQL: a customer bought a variant one of their orders carries a
// line of, unless that order was canceled; another customer's order, a guest
// order and a variant nobody ordered are no purchase.
func TestACustomerBoughtWhatTheirOrdersCarry(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	buyer := "cust_BOUGHT_" + models.NewOrderID()
	canceler := "cust_CANCELED_" + models.NewOrderID()

	placeOrderFor(t, svc, buyer, "buyer@example.test")
	canceled := placeOrderFor(t, svc, canceler, "canceler@example.test")
	require.NoError(t, svc.CancelOrder(ctx, canceled.ID, "changed their mind"))
	guest := validInput()
	guest.CustomerID = ""
	guest.Email = "guest@example.test"
	_, err := svc.CreateOrder(ctx, guest)
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		customer string
		variants []string
		bought   bool
	}{
		"a line of their order":          {buyer, []string{"variant_A"}, true},
		"one of several variants":        {buyer, []string{"variant_Z", "variant_A"}, true},
		"a variant they did not order":   {buyer, []string{"variant_Z"}, false},
		"their only order was canceled":  {canceler, []string{"variant_A"}, false},
		"a customer who ordered nothing": {"cust_NOBODY_" + models.NewOrderID(), []string{"variant_A"}, false},
		"no variants is no purchase":     {buyer, nil, false},
	} {
		bought, err := svc.CustomerBoughtAnyOf(ctx, tc.customer, tc.variants)
		require.NoError(t, err, name)
		assert.Equal(t, tc.bought, bought, name)
	}

	_, err = svc.CustomerBoughtAnyOf(ctx, "", []string{"variant_A"})
	require.Error(t, err, "a purchase is asked of a customer")
	_, err = service.NewInterop(svc).CustomerBoughtAnyOf(ctx, buyer, []string{"variant_A"})
	require.NoError(t, err, "and the interop surface answers it")
}
