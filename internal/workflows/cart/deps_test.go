package cart

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
)

// provideAll registers the fakes of all six surfaces in a container UNDER THEIR
// REAL NAMES.
func provideAll(t *testing.T, c *container.Container, h *harness) {
	t.Helper()

	require.NoError(t, c.Provide(ServiceCart, h.carts))
	require.NoError(t, c.Provide(ServicePricing, h.prices))
	require.NoError(t, c.Provide(ServiceRegion, h.regions))
	require.NoError(t, c.Provide(ServiceCustomer, h.customers))
	require.NoError(t, c.Provide(ServiceLink, h.links))
	require.NoError(t, c.Provide(ServiceQuery, h.catalog))
}

// TestFromContainerResolvesByName verifies that the dependencies are resolved
// from the container BY NAME and that the resolved workflows work (ADR 0006).
func TestFromContainerResolvesByName(t *testing.T) {
	h := newHarness(t)
	recordOpenCart(h.carts, testCartID)

	c := container.New(nil)
	provideAll(t, c, h)

	wf, err := FromContainer(c)
	require.NoError(t, err)

	out, err := wf.CreateCart(context.Background(), CreateCartInput{CountryCode: "TR"})
	require.NoError(t, err)
	assert.Equal(t, testCartID, out.CartID)
}

// TestFromContainerReportsMissingService verifies that an unregistered name
// produces a diagnosable error.
//
// This is the accepted price of ADR 0006: the mismatch is caught not at compile
// time but at resolution time — so the error must write WHICH name was looked up
// and not found.
func TestFromContainerReportsMissingService(t *testing.T) {
	h := newHarness(t)
	c := container.New(nil)
	require.NoError(t, c.Provide(ServiceCart, h.carts))

	_, err := FromContainer(c)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
	assert.Equal(t, CodeDependencyMissing, errors.CodeOf(err))
	assert.Contains(t, err.Error(), ServicePricing)
}

// TestFromContainerReportsMismatchedType verifies that a service that is
// registered but does not satisfy the surface is not silently accepted.
func TestFromContainerReportsMismatchedType(t *testing.T) {
	h := newHarness(t)
	c := container.New(nil)
	provideAll(t, c, h)

	mismatched := container.New(nil)
	// A value that DOES NOT SATISFY the Carts surface is put under the
	// ServiceCart name; that is exactly the state of the cart module's service
	// today (see the package comment).
	require.NoError(t, mismatched.Provide(ServiceCart, h.regions))
	require.NoError(t, mismatched.Provide(ServicePricing, h.prices))
	require.NoError(t, mismatched.Provide(ServiceRegion, h.regions))
	require.NoError(t, mismatched.Provide(ServiceCustomer, h.customers))
	require.NoError(t, mismatched.Provide(ServiceLink, h.links))
	require.NoError(t, mismatched.Provide(ServiceQuery, h.catalog))

	_, err := FromContainer(mismatched)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodeDependencyMissing, errors.CodeOf(err))
	assert.Contains(t, err.Error(), ServiceCart)
}

// TestFromContainerRejectsNilContainer verifies that a nil container produces an
// error, not a panic.
func TestFromContainerRejectsNilContainer(t *testing.T) {
	_, err := FromContainer(nil)
	require.Error(t, err)
	assert.Equal(t, CodeNotReady, errors.CodeOf(err))
}

// TestNewRejectsMissingDependency verifies that a missing surface errors at
// WIRING time.
func TestNewRejectsMissingDependency(t *testing.T) {
	full := func(h *harness) Deps {
		return Deps{
			Carts:     h.carts,
			Prices:    h.prices,
			Regions:   h.regions,
			Customers: h.customers,
			Links:     h.links,
			Catalog:   h.catalog,
		}
	}

	tests := map[string]func(*Deps){
		ServiceCart:     func(d *Deps) { d.Carts = nil },
		ServicePricing:  func(d *Deps) { d.Prices = nil },
		ServiceRegion:   func(d *Deps) { d.Regions = nil },
		ServiceCustomer: func(d *Deps) { d.Customers = nil },
		ServiceLink:     func(d *Deps) { d.Links = nil },
		ServiceQuery:    func(d *Deps) { d.Catalog = nil },
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

// TestNewWiresWithoutLogger verifies that the workflows still work when no
// logger is given.
func TestNewWiresWithoutLogger(t *testing.T) {
	h := newHarness(t)
	recordOpenCart(h.carts, testCartID)

	wf, err := New(Deps{
		Carts:     h.carts,
		Prices:    h.prices,
		Regions:   h.regions,
		Customers: h.customers,
		Links:     h.links,
		Catalog:   h.catalog,
	})
	require.NoError(t, err)

	_, err = wf.CreateCart(context.Background(), CreateCartInput{CountryCode: "TR"})
	require.NoError(t, err)
}

// TestFromContainerResolvesOptionalSurfaces verifies that promotion and tax, if
// registered, are resolved and that they ARE USED in the computation.
//
// Saying only "no error was returned" would not have been enough: a surface
// resolved under the wrong name also wires without an error and the cart would
// silently run along the degraded path. The assertion therefore rests on the tax
// source in the result.
func TestFromContainerResolvesOptionalSurfaces(t *testing.T) {
	h := newModuleHarness(t)
	h.taxes.rateBps = 1000
	serveSnapshot(h.carts, twoLineCart(1))

	c := container.New(nil)
	provideAll(t, c, h)
	require.NoError(t, c.Provide(ServicePromotion, h.discounts))
	require.NoError(t, c.Provide(ServiceTax, h.taxes))

	wf, err := FromContainer(c)
	require.NoError(t, err)

	totals, err := wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)
	assert.Equal(t, TaxSourceTax, totals.TaxSource)
	assert.Equal(t, int64(275), totals.TaxTotal, "the tax rate is 10%; the region carries 20%")
}

// TestFromContainerWiresWithoutOptionalSurfaces verifies that WITH promotion and
// tax UNREGISTERED the workflows are still wired and run along the degraded
// path.
//
// Had they been counted mandatory, in a deployment that does not install these
// two modules the cart would not have worked at all; modularity means exactly
// that this is possible.
func TestFromContainerWiresWithoutOptionalSurfaces(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, twoLineCart(1))

	c := container.New(nil)
	provideAll(t, c, h)

	wf, err := FromContainer(c)
	require.NoError(t, err)

	totals, err := wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)
	assert.Equal(t, TaxSourceRegion, totals.TaxSource)
	assert.Zero(t, totals.DiscountTotal)
}

// TestFromContainerReportsMismatchedOptionalType verifies that an optional
// service that is registered but DOES NOT SATISFY the surface is not silently
// ignored.
//
// The distinction is critical: "not registered" is a deployment decision,
// "registered but of the wrong type" is a wiring error. Had the second one
// degraded, a wrongly registered tax module would have stayed invisible forever.
func TestFromContainerReportsMismatchedOptionalType(t *testing.T) {
	for _, name := range []string{ServicePromotion, ServiceTax} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			c := container.New(nil)
			provideAll(t, c, h)
			require.NoError(t, c.Provide(name, h.regions))

			_, err := FromContainer(c)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
			assert.Equal(t, CodeDependencyMissing, errors.CodeOf(err))
			assert.Contains(t, err.Error(), name)
		})
	}
}

// TestNewWiresWithoutOptionalDependency verifies that a missing optional surface
// does not bring the WIRING down; the test for the mandatory surfaces is
// TestNewRejectsMissingDependency.
func TestNewWiresWithoutOptionalDependency(t *testing.T) {
	h := newHarness(t)

	wf, err := New(Deps{
		Carts:     h.carts,
		Prices:    h.prices,
		Regions:   h.regions,
		Customers: h.customers,
		Links:     h.links,
		Catalog:   h.catalog,
	})
	require.NoError(t, err)
	assert.NotNil(t, wf)
}
