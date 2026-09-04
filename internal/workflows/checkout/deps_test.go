package checkout

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// Service names that the cart totals resolve from the container.
//
// This package does not resolve them BY NAME; the names are needed only to
// register the dependencies of the cart totals that [FromContainer] builds.
const (
	svcPricing  = "pricing.service"
	svcRegion   = "region.service"
	svcCustomer = "customer.service"
)

// stubPricing satisfies the pricing surface of the cart totals.
type stubPricing struct{}

// CalculateAmount returns the unit amount of the price set.
func (stubPricing) CalculateAmount(
	_ context.Context,
	priceSetID, _ string,
	_ int32,
	_ map[string]string,
) (int64, error) {
	switch priceSetID {
	case testPriceSetA:
		return 1000, nil
	case testPriceSetB:
		return 500, nil
	default:
		return 0, errUnexpected("CalculateAmount: " + priceSetID)
	}
}

// CalculateAmountsJSON satisfies the BULK pricing surface of the cart totals.
//
// The response is in the same order and at the same length as the request; an
// item with no price is reported not as an error but with a flag (that is the
// contract of the real pricing module).
func (s stubPricing) CalculateAmountsJSON(
	ctx context.Context,
	request json.RawMessage,
) (json.RawMessage, error) {
	var req struct {
		Items []struct {
			PriceSetID string `json:"price_set_id"`
			Quantity   int32  `json:"quantity"`
		} `json:"items"`
	}
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}

	type item struct {
		Amount int64 `json:"amount"`
		Priced bool  `json:"priced"`
	}
	out := struct {
		Items []item `json:"items"`
	}{Items: make([]item, 0, len(req.Items))}

	for i := range req.Items {
		amount, err := s.CalculateAmount(ctx, req.Items[i].PriceSetID, "", req.Items[i].Quantity, nil)
		if err != nil {
			out.Items = append(out.Items, item{})
			continue
		}
		out.Items = append(out.Items, item{Amount: amount, Priced: true})
	}
	return json.Marshal(out)
}

// stubRegions satisfies the region surface of the cart totals.
type stubRegions struct{}

// RegionIDForCountry is not called in this flow.
func (stubRegions) RegionIDForCountry(_ context.Context, _ string) (string, error) {
	return "", errUnexpected("RegionIDForCountry")
}

// RegionCurrency is not called in this flow.
func (stubRegions) RegionCurrency(_ context.Context, _ string) (code string, decimalDigits int32, err error) {
	return "", 0, errUnexpected("RegionCurrency")
}

// RegionTax reports the automatic 20% tax.
func (stubRegions) RegionTax(_ context.Context, _ string) (rateBps int32, automatic bool, err error) {
	return 2000, true, nil
}

// stubCustomers satisfies the customer surface of the cart totals.
type stubCustomers struct{}

// CustomerEmail is not called in this flow.
func (stubCustomers) CustomerEmail(_ context.Context, _ string) (string, error) {
	return "", errUnexpected("CustomerEmail")
}

// provideCheckout registers this workflow's own surfaces in the container.
func provideCheckout(t *testing.T, c *container.Container, h *harness) {
	t.Helper()

	require.NoError(t, c.Provide(ServiceCart, h.carts))
	require.NoError(t, c.Provide(ServiceInventory, h.inventory))
	require.NoError(t, c.Provide(ServiceFulfillment, h.fulfillment))
	require.NoError(t, c.Provide(ServiceOrder, h.orders))
	require.NoError(t, c.Provide(ServicePayment, h.payments))
	require.NoError(t, c.Provide(ServiceLink, h.links))
	require.NoError(t, c.Provide(ServiceQuery, h.catalog))
	require.NoError(t, c.Provide(ServiceWorkflow, workflow.NewInMemory(slog.New(slog.DiscardHandler))))
}

// provideCartTotals registers the dependencies of the cart totals in the
// container.
func provideCartTotals(t *testing.T, c *container.Container) {
	t.Helper()

	require.NoError(t, c.Provide(svcPricing, stubPricing{}))
	require.NoError(t, c.Provide(svcRegion, stubRegions{}))
	require.NoError(t, c.Provide(svcCustomer, stubCustomers{}))
}

// TestFromContainerResolvesByName verifies that the dependencies are resolved
// from the container BY NAME and that the resolved workflow runs end to end
// (ADR 0006).
//
// The test also exercises the SINGLE compile-time bond between the packages:
// what produces the totals is the REAL cart flow (not a fake of it) and the
// line amounts it produces pass into the order as they are. The cart carries
// two lines with 20% tax; the totals must produce 2500 + 500 = 3000.
func TestFromContainerResolvesByName(t *testing.T) {
	h := newHarness(t)
	h.carts.setTotalsFn = func(context.Context, string) error { return nil }

	c := container.New(nil)
	provideCheckout(t, c, h)
	provideCartTotals(t, c)

	wf, err := FromContainer(c)
	require.NoError(t, err)

	out, err := wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.Equal(t, testOrderID, out.OrderID)
	assert.Equal(t, testAmount, out.Amount)
	require.Len(t, h.orders.placed, 1)
	assert.Equal(t, testAmount, h.orders.placed[0].Total)
	assert.Equal(t, int64(1000), h.orders.placed[0].Items[0].UnitPrice)
}

// TestFromContainerReportsMissingService verifies that an unregistered name
// produces a diagnosable error.
//
// This is the accepted price of ADR 0006: the mismatch is caught not at compile
// time but at resolution time — so the error must write down WHICH name was
// looked up and not found.
func TestFromContainerReportsMissingService(t *testing.T) {
	h := newHarness(t)
	c := container.New(nil)
	require.NoError(t, c.Provide(ServiceCart, h.carts))

	_, err := FromContainer(c)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
	assert.Equal(t, CodeDependencyMissing, errors.CodeOf(err))
	assert.Contains(t, err.Error(), ServiceInventory)
}

// TestFromContainerReportsIncompatibleType verifies that a service which is
// registered but does not satisfy the surface is not accepted silently.
func TestFromContainerReportsIncompatibleType(t *testing.T) {
	h := newHarness(t)
	c := container.New(nil)

	require.NoError(t, c.Provide(ServiceCart, h.carts))
	require.NoError(t, c.Provide(ServiceInventory, h.inventory))
	require.NoError(t, c.Provide(ServiceFulfillment, h.fulfillment))
	// A value that does NOT satisfy the [Orders] surface is put under the name
	// "order.interop".
	require.NoError(t, c.Provide(ServiceOrder, h.links))
	require.NoError(t, c.Provide(ServicePayment, h.payments))
	require.NoError(t, c.Provide(ServiceLink, h.links))
	require.NoError(t, c.Provide(ServiceQuery, h.catalog))
	require.NoError(t, c.Provide(ServiceWorkflow, workflow.NewInMemory(slog.New(slog.DiscardHandler))))

	_, err := FromContainer(c)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodeDependencyMissing, errors.CodeOf(err))
	assert.Contains(t, err.Error(), ServiceOrder)
}

// TestFromContainerReportsUnbuildableCartTotals verifies that the error is
// understandable when the dependencies of the totals are missing.
func TestFromContainerReportsUnbuildableCartTotals(t *testing.T) {
	h := newHarness(t)
	c := container.New(nil)
	provideCheckout(t, c, h)

	_, err := FromContainer(c)
	require.Error(t, err)
	assert.Equal(t, CodeDependencyMissing, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "could not build the cart totals")
}

// TestFromContainerRejectsNilContainer verifies that a nil container produces
// an error rather than a panic.
func TestFromContainerRejectsNilContainer(t *testing.T) {
	_, err := FromContainer(nil)
	require.Error(t, err)
	assert.Equal(t, CodeNotReady, errors.CodeOf(err))
}

// TestNewRejectsMissingDependency verifies that a missing surface fails at
// SETUP time.
func TestNewRejectsMissingDependency(t *testing.T) {
	full := func(h *harness) Deps {
		return Deps{
			Carts:       h.carts,
			Totals:      h.totals,
			Inventory:   h.inventory,
			Fulfillment: h.fulfillment,
			Orders:      h.orders,
			Payments:    h.payments,
			Links:       h.links,
			Catalog:     h.catalog,
			Executor:    workflow.NewInMemory(slog.New(slog.DiscardHandler)),
		}
	}

	tests := map[string]func(*Deps){
		ServiceCart:        func(d *Deps) { d.Carts = nil },
		serviceCartTotals:  func(d *Deps) { d.Totals = nil },
		ServiceInventory:   func(d *Deps) { d.Inventory = nil },
		ServiceFulfillment: func(d *Deps) { d.Fulfillment = nil },
		ServiceOrder:       func(d *Deps) { d.Orders = nil },
		ServicePayment:     func(d *Deps) { d.Payments = nil },
		ServiceLink:        func(d *Deps) { d.Links = nil },
		ServiceQuery:       func(d *Deps) { d.Catalog = nil },
		ServiceWorkflow:    func(d *Deps) { d.Executor = nil },
	}

	for name, drop := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			deps := full(h)
			drop(&deps)

			_, err := New(deps)
			require.Error(t, err)
			assert.Equal(t, CodeNotReady, errors.CodeOf(err))
			assert.Contains(t, err.Error(), name)
		})
	}
}

// TestNewBuildsWithoutLogger verifies that the workflow still runs when no
// logger is given.
func TestNewBuildsWithoutLogger(t *testing.T) {
	h := newHarness(t)

	wf, err := New(Deps{
		Carts:       h.carts,
		Totals:      h.totals,
		Inventory:   h.inventory,
		Fulfillment: h.fulfillment,
		Orders:      h.orders,
		Payments:    h.payments,
		Links:       h.links,
		Catalog:     h.catalog,
		Executor:    workflow.NewInMemory(slog.New(slog.DiscardHandler)),
	})
	require.NoError(t, err)

	out, err := wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)
	assert.Equal(t, testOrderID, out.OrderID)
}
