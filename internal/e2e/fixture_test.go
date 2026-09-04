//go:build integration

package e2e

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"

	customersvc "github.com/bdrtr/gobit/internal/modules/customer/service"
	inventorymodels "github.com/bdrtr/gobit/internal/modules/inventory/models"
	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// fixtureCounter lets the fixtures generate unique handles and email addresses.
//
// The tests share a single database, and both a product handle and a registered
// customer's email address are UNIQUE; using a fixed name would make the tests
// collide with one another depending on the order they happen to run in.
var fixtureCounter atomic.Int64

// newVariant creates a product and a variant, sets up a price set and links it
// to the variant when one is asked for, and returns the VARIANT ID.
//
// When prices is nil the variant is NOT LINKED to any price set; that is the
// setup for the "variant without a price" scenario. In a non-empty map the
// currency codes are written in SORTED order, so the same fixture is built in
// the same order on every run.
//
// The price set bond is made with the "product_variant_price_set" link; the
// cart flow finds the variant's price through exactly that link (see
// workflows/cart priceSetsFor).
func newVariant(ctx context.Context, t *testing.T, title string, prices map[string]int64) string {
	t.Helper()

	seq := fixtureCounter.Add(1)
	product, err := productSvc.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle: fmt.Sprintf("e2e-product-%d", seq),
		Title:  title,
		Status: productmodels.StatusPublished,
	})
	require.NoError(t, err, "could not create the fixture product")

	variant, err := productSvc.CreateVariant(ctx, product.ID, productsvc.CreateVariantInput{Title: title})
	require.NoError(t, err, "could not create the fixture variant")

	if len(prices) == 0 {
		return variant.ID
	}

	inputs := make([]pricingsvc.PriceInput, 0, len(prices))
	for _, code := range slices.Sorted(maps.Keys(prices)) {
		inputs = append(inputs, pricingsvc.PriceInput{
			CurrencyCode: code,
			Amount:       prices[code],
			MinQuantity:  1,
		})
	}

	set, err := pricingSvc.CreatePriceSet(ctx, inputs)
	require.NoError(t, err, "could not create the fixture price set")
	require.NoError(t, productSvc.SetVariantPriceSet(ctx, variant.ID, set.ID),
		"could not link the variant to the price set; without the bond the flow finds no price")

	return variant.ID
}

// newCustomer creates a REGISTERED customer and returns its id along with its
// email address.
func newCustomer(ctx context.Context, t *testing.T) (customerID, email string) {
	t.Helper()

	seq := fixtureCounter.Add(1)
	email = fmt.Sprintf("e2e-customer-%d@example.test", seq)
	customer, err := customerSvc.CreateCustomer(ctx, customersvc.CustomerInput{
		Email:     email,
		FirstName: "E2E",
		LastName:  "Customer",
	})
	require.NoError(t, err, "could not create the fixture customer")

	return customer.ID, customer.Email
}

// newStockedVariant sets up a variant that has both a price AND stock; it
// returns the ids of the variant and of the inventory item.
//
// The setup has four parts and all four live in the real modules: the variant +
// its price (see [newVariant]), the inventory item, the variant -> item link
// and the stock level at the shared location. The order completion flow finds
// the inventory item through exactly the "product_variant_inventory" bond (see
// checkoutwf.plan.go); if the bond is not made the flow counts the variant as
// "out of stock" and the cart can never become an order.
//
// stock is the PHYSICAL quantity at the location. The reserved quantity starts
// at zero, which means the sellable quantity also starts out equal to the
// stock.
func newStockedVariant(
	ctx context.Context,
	t *testing.T,
	title string,
	prices map[string]int64,
	stock int64,
) (variantID, inventoryItemID string) {
	t.Helper()

	variantID = newVariant(ctx, t, title, prices)

	seq := fixtureCounter.Add(1)
	item, err := inventorySvc.CreateInventoryItem(ctx, inventorysvc.CreateInventoryItemInput{
		SKU:   fmt.Sprintf("E2E-SKU-%d", seq),
		Title: title,
	})
	require.NoError(t, err, "could not create the fixture inventory item")

	require.NoError(t, productSvc.SetVariantInventoryItem(ctx, variantID, item.ID),
		"could not link the variant to the inventory item; without the bond the flow counts the variant as out of stock")

	level, err := inventorySvc.SetInventoryLevel(ctx, item.ID, stockLocationID, stock)
	require.NoError(t, err, "could not write the fixture stock level")
	require.Equal(t, stock, level.Available(),
		"on a fresh level the sellable quantity must equal the physical quantity; if it "+
			"does not, the fixture is already carrying reserved stock from the start")

	return variantID, item.ID
}

// sellableQuantity returns the inventory item's sellable total across ALL
// locations: stocked - reserved.
//
// The number is read from the inventory module, not from a value the flow
// returned: the claim under test is that the reservation really moved in the
// inventory module's LEDGER.
func sellableQuantity(ctx context.Context, t *testing.T, inventoryItemID string) int64 {
	t.Helper()

	quantity, err := inventorySvc.AvailableQuantity(ctx, inventoryItemID)
	require.NoError(t, err, "could not read the sellable quantity")
	return quantity
}

// stockLevel returns the item's level at the SHARED location.
//
// The sellable quantity alone is not enough: confirming a reservation (deducting
// it from stock) and releasing it back can be told apart in terms of the
// sellable quantity, but in terms of the PHYSICAL quantity they are exact
// opposites. Seeing both numbers at once makes the difference between "stock
// went down" and "stock came back" definite.
func stockLevel(ctx context.Context, t *testing.T, inventoryItemID string) inventorymodels.InventoryLevel {
	t.Helper()

	levels, err := inventorySvc.ListInventoryLevels(ctx, inventoryItemID)
	require.NoError(t, err, "could not read the stock levels")
	require.Len(t, levels, 1,
		"the fixture item must be leveled at a SINGLE location; a second level would "+
			"blur which warehouse the quantities came from")
	return levels[0]
}
