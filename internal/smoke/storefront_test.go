//go:build smoke

package smoke

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
)

// This file pins down that the STOREFRONT PATH is open in the real binary: open a
// cart -> add a line item -> complete -> order.
//
// # Why a smoke scenario and not a static audit
//
// TestEveryWorkflowIsSetUpInTheCompositionRoot in internal/arch says "the
// workflows' constructor is called in the composition root, somewhere reachable
// from main()", and that is what can be read in the source. Whether the call RUNS
// is something static analysis cannot answer: once the setup is put behind a
// condition ("if enabled { … }") that audit PASSES, while in the running binary no
// line item can be added to a cart and no cart can be turned into an order — both
// endpoints return 500 (the cart module cannot resolve the workflow and fails
// CLOSED). So the very fault that was found slips under the invariant, in a more
// insidious form.
//
// This scenario closes that gap by USING the path. Flag, condition, function
// variable — whichever shape you switch the setup off in, the cart endpoint does
// not answer and the test fails. In exchange it cannot give what the static
// invariant gives: when the setup line is DELETED this test only says "the line
// item could not be added, 500"; it does not say which package was not set up, and
// to be able to say that it needs docker, two containers and a startup.
// The two layers therefore stand together; the whole rationale is in the
// invariant's godoc.
//
// # Why internal/e2e is not enough
//
// internal/e2e/storefront_flow_test.go drives the same chain over HTTP but wires
// the router itself with httptest: it SKIPS cmd/server's wiring, the startup order
// and the real process. Putting the setup behind a flag would not have failed that
// test — because that test sets the workflows up on its own ground. The process
// here, by contrast, runs the `go build` output, so main() is what decides.

// The HAND-computed amounts of the storefront scenario (minor unit).
//
// The region is taxed at 20% (2000 basis points) and no shipping method is
// selected:
//
//	32_000 x 2 = 64_000 subtotal
//	64_000 x 20% = 12_800 tax
//	64_000 - 0 + 12_800 + 0 = 76_800 grand total
//
// The numbers are DELIBERATELY the same as in the storefront scenario in
// internal/e2e: if a difference shows up between the two runs, the source of the
// difference is not the arithmetic but the WIRING, and the place to look narrows.
const (
	storefrontUnitPrice int64 = 32_000
	storefrontQuantity  int64 = 2
	storefrontSubtotal  int64 = 64_000
	storefrontTax       int64 = 12_800
	storefrontTotal     int64 = 76_800
	// storefrontTaxBps is the region's fallback tax rate (2000 = 20%).
	storefrontTaxBps int32 = 2_000
	// storefrontStock is the physical quantity at the location; it must be MORE
	// than what the order will reserve, so that the scenario fails for the reason
	// it tests and not with "out of stock".
	storefrontStock int64 = 5
)

// storefrontCurrency is the currency of the scenario's region and prices.
//
// The code is registered in the region module's seed; an unseeded code would blow
// the region up not at startup but on the first cart request.
const storefrontCurrency = "TRY"

// storefrontCountries are the countries bound to the scenario's region; the FIRST
// one is sent when the cart is opened.
//
// The rationale for there being two is in the [openStorefrontRegion] godoc. The
// codes are registered in the region module's seed; an unseeded code cannot be
// bound to a region.
var storefrontCountries = []string{"TR", "AZ"}

// storefrontVariantTitle is the title of the variant in the catalog.
//
// A line item's title is copied FROM THE CATALOG and the client cannot send a
// title; reading the constant back in the scenario is the proof that the copy was
// really made.
const storefrontVariantTitle = "Smoke Storefront Product"

// storefrontLineItem holds the cart and order line item fields the scenario reads.
//
// The modules' DTO types are NOT imported; the rationale is in the [zarfVerisi]
// documentation.
type storefrontLineItem struct {
	ID        string `json:"id"`
	VariantID string `json:"variant_id"`
	Title     string `json:"title"`
	Quantity  int64  `json:"quantity"`
	UnitPrice int64  `json:"unit_price"`
	Subtotal  int64  `json:"subtotal"`
}

// storefrontCart holds the cart body fields the scenario reads.
type storefrontCart struct {
	ID           string               `json:"id"`
	RegionID     string               `json:"region_id"`
	CurrencyCode string               `json:"currency_code"`
	Subtotal     int64                `json:"subtotal"`
	TaxTotal     int64                `json:"tax_total"`
	Total        int64                `json:"total"`
	TotalsStale  bool                 `json:"totals_stale"`
	Items        []storefrontLineItem `json:"items"`
}

// storefrontCompletionResult is the POST /store/v1/carts/{id}/complete response.
type storefrontCompletionResult struct {
	OrderID      string `json:"order_id"`
	CartID       string `json:"cart_id"`
	CurrencyCode string `json:"currency_code"`
	Total        int64  `json:"total"`
}

// storefrontOrder holds the order body fields the scenario reads.
type storefrontOrder struct {
	ID           string               `json:"id"`
	Status       string               `json:"status"`
	CartID       string               `json:"cart_id"`
	CurrencyCode string               `json:"currency_code"`
	Email        string               `json:"email"`
	Subtotal     int64                `json:"subtotal"`
	TaxTotal     int64                `json:"tax_total"`
	Total        int64                `json:"total"`
	Items        []storefrontLineItem `json:"items"`
}

// TestStorefrontFromCartToOrderInARealProcess proves that the storefront path is
// OPEN in the real binary.
//
// The chain is HTTP throughout and runs in a single process: cold start -> admin
// identity -> sales channel + publishable key -> region -> catalog (product,
// variant, price, stock) -> cart -> line item -> completion -> reading the order
// back from the admin endpoint.
//
// # Which mutations it catches
//
// EVERY change that closes the path, whatever its shape:
//
//   - switching the workflow setup off with "if false" (adding a line item 500s),
//   - deleting the setup line (the same),
//   - deleting the cart module's route registration (opening a cart 404s),
//   - breaking the pricer (no line item is added, the price does not come from the
//     catalog).
//
// The only thing it cannot catch is the path being OPEN but set up in a shape the
// audit cannot see — that question is the static invariant's job and the rationale
// is written there.
//
// # Why the order is read from the admin endpoint
//
// The order_id in the completion response does not on its own mean "an order was
// created": the workflow may have produced an id and failed to write the record.
// Reading it back from the admin endpoint confirms, from inside the production
// guard stack, that the record really is durable and that its amounts are the same
// as the cart's.
func TestStorefrontFromCartToOrderInARealProcess(t *testing.T) {
	dsn := scenarioDatabase(t)

	cfg := baseSettings(dsn, freePort(t))
	cfg["ADMIN_BOOTSTRAP_EMAIL"] = seedEmail
	cfg["ADMIN_BOOTSTRAP_PASSWORD"] = seedPassword

	s := startServer(t, cfg)
	s.waitForReady(startupTimeout)

	token, _, storefrontKey := setUpAdminHarness(t, s, "Smoke Storefront Channel")
	regionID := openStorefrontRegion(t, s, token)
	variantID := setUpStorefrontCatalog(t, s, token)

	const email = "smoke-storefront@example.test"
	cartID := openStorefrontCart(t, s, storefrontKey, regionID, email)

	lineItemPath := "/store/v1/carts/" + cartID + "/line-items"

	t.Run("a line item is added and the price comes FROM THE CATALOG", func(t *testing.T) {
		// There is NO price and NO title in the body: the server writes both. If
		// the workflow is not set up this request gets a 500 and the scenario
		// stops right here.
		status, body := s.storefrontRequest(http.MethodPost, lineItemPath, storefrontKey,
			map[string]any{"variant_id": variantID, "quantity": storefrontQuantity})
		require.Equal(t, http.StatusCreated, status,
			"the line item could not be added to the cart. 500 + cart_module_setup_failed "+
				"says the workflow COULD NOT BE RESOLVED from the container (failing "+
				"closed: no line item is written until the server has set the price). "+
				"A 404 says the endpoint IS NOT MOUNTED; the status code tells the two "+
				"apart. body: %s", body)

		item := zarfVerisi[storefrontLineItem](t, body)
		assert.Equal(t, storefrontUnitPrice, item.UnitPrice,
			"the unit price must come from the catalog; the client sent no price at all")
		assert.Equal(t, storefrontVariantTitle, item.Title,
			"the title must be copied from the catalog too; the client does not send a title either")
		assert.Equal(t, storefrontSubtotal, item.Subtotal,
			"the line item's subtotal must be written during the totals pass")
	})

	t.Run("the cart's totals come back FRESH and taxed", func(t *testing.T) {
		status, body := s.storefrontRequest(http.MethodGet, "/store/v1/carts/"+cartID, storefrontKey, nil)
		require.Equal(t, http.StatusOK, status, "the cart could not be read; body: %s", body)

		cart := zarfVerisi[storefrontCart](t, body)
		assert.Equal(t, storefrontSubtotal, cart.Subtotal)
		assert.Equal(t, storefrontTax, cart.TaxTotal,
			"the tax must be computed with the region's rate; had the totals pass not run it would have stayed zero")
		assert.Equal(t, storefrontTotal, cart.Total)
		assert.False(t, cart.TotalsStale,
			"after a line item is added the totals must be FRESH; a stale total cannot be ordered")
		require.Len(t, cart.Items, 1, "the cart must hold a single line item")
	})

	var orderID string

	t.Run("the cart is turned into an order", func(t *testing.T) {
		status, body := s.storefrontRequest(http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
			storefrontKey, map[string]any{
				"payment_provider_id": manual.ID,
				"payment_data":        map[string]any{manual.DataKeyOutcome: manual.OutcomeAuthorize},
				"expected_total":      storefrontTotal,
			})
		require.Equal(t, http.StatusOK, status,
			"the cart could not be completed. 500 + cart_module_setup_failed says the "+
				"order workflow could not be resolved from the container; a 404 says the "+
				"completion endpoint is not mounted. The status code says which one it "+
				"is. body: %s", body)

		result := zarfVerisi[storefrontCompletionResult](t, body)
		require.NotEmpty(t, result.OrderID, "the response must carry the order's id; body: %s", body)
		assert.Equal(t, cartID, result.CartID, "the result must record the cart it was born from")
		assert.Equal(t, storefrontCurrency, result.CurrencyCode)
		assert.Equal(t, storefrontTotal, result.Total,
			"the amount charged must be the HAND-computed grand total")

		orderID = result.OrderID
	})

	t.Run("the order is durable and its amounts match the cart's", func(t *testing.T) {
		require.NotEmpty(t, orderID, "the previous step produced no order; this step is meaningless")

		status, body := s.adminRequest(http.MethodGet, "/admin/v1/orders/"+orderID, token, nil)
		require.Equal(t, http.StatusOK, status, "the order could not be read from the admin endpoint; body: %s", body)

		order := zarfVerisi[storefrontOrder](t, body)
		assert.Equal(t, string(ordermodels.OrderPending), order.Status)
		assert.Equal(t, cartID, order.CartID, "the order must record the cart it was born from")
		assert.Equal(t, email, order.Email,
			"the contact address must come FROM THE CART; the completion body carries no email")
		assert.Equal(t, storefrontSubtotal, order.Subtotal)
		assert.Equal(t, storefrontTax, order.TaxTotal)
		assert.Equal(t, storefrontTotal, order.Total)

		require.Len(t, order.Items, 1, "the cart's single line item must carry over to the order as a single line item")
		assert.Equal(t, storefrontUnitPrice, order.Items[0].UnitPrice,
			"the unit price on the order must also be the price that came from the catalog")
		assert.Equal(t, storefrontVariantTitle, order.Items[0].Title)
		assert.Equal(t, storefrontQuantity, order.Items[0].Quantity)
	})

	t.Run("no second line item can be added to a completed cart", func(t *testing.T) {
		// The proof that the cart is CLOSED: had it not closed, the same cart
		// could have been the source of a second order. The assertion looks at the
		// cart's BEHAVIOR and not at its flag, because behavior is what the
		// storefront client sees.
		status, body := s.storefrontRequest(http.MethodPost, lineItemPath, storefrontKey,
			map[string]any{"variant_id": variantID, "quantity": int64(1)})
		assert.Equal(t, http.StatusConflict, status,
			"a completed cart must not be modifiable; body: %s", body)
	})
}

// openStorefrontRegion opens the scenario's taxed region and returns its id.
//
// TWO countries are bound to the region and the number is deliberate. Binding one
// is MANDATORY: the cart-opening endpoint derives the region from the country, and
// no cart at all can be opened in a region with no country. The second one pins
// down where the tax comes from — in a region whose country does not resolve to a
// single code the tax is computed with the region's own rate instead of by the tax
// module (see workflows/cart countryForRegion). What the scenario tests is not
// which module the tax came from but that the totals pass RAN; setting up a tax
// region would have added three more requests to the scenario that have nothing to
// do with that question.
func openStorefrontRegion(t *testing.T, s *proc, token string) string {
	t.Helper()

	status, body := s.adminRequest(http.MethodPost, "/admin/v1/regions", token, map[string]any{
		"name":            "Smoke Storefront Region",
		"currency_code":   storefrontCurrency,
		"automatic_taxes": true,
		"tax_rate_bps":    storefrontTaxBps,
	})
	require.Equal(t, http.StatusCreated, status, "the region could not be opened; body: %s", body)

	region := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, body)
	require.NotEmpty(t, region.ID, "the region must return an id; body: %s", body)

	for _, country := range storefrontCountries {
		status, body = s.adminRequest(http.MethodPost, "/admin/v1/regions/"+region.ID+"/countries",
			token, map[string]any{"country_code": country})
		require.Equal(t, http.StatusCreated, status,
			"country %s could not be bound to the region; without the binding the storefront cannot open a cart at all. body: %s",
			country, body)
	}

	return region.ID
}

// setUpStorefrontCatalog sets up a variant that has a price AND stock; it returns
// the variant's id.
//
// The setup is five parts and all five go THROUGH THE ADMIN ENDPOINTS: product +
// variant, price set and its binding to the variant, stock location, inventory
// item and its binding to the variant, the physical quantity at the location. The
// bindings are mandatory — a variant with no price binding cannot enter a cart,
// and a variant with no stock binding is counted "out of stock" by the workflow
// and its cart can never become an order.
func setUpStorefrontCatalog(t *testing.T, s *proc, token string) string {
	t.Helper()

	variantID := createStorefrontVariant(t, s, token)
	bindStorefrontPrice(t, s, token, variantID)
	bindStorefrontStock(t, s, token, variantID)

	return variantID
}

// createStorefrontVariant opens a product and one variant under it; it returns the
// variant's id.
func createStorefrontVariant(t *testing.T, s *proc, token string) string {
	t.Helper()

	status, body := s.adminRequest(http.MethodPost, "/admin/v1/products", token, map[string]any{
		"handle": "smoke-storefront-product",
		"title":  storefrontVariantTitle,
		"status": "published",
	})
	require.Equal(t, http.StatusCreated, status, "the product could not be opened; body: %s", body)

	productID := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, body).ID
	require.NotEmpty(t, productID, "the product must return an id; body: %s", body)

	status, body = s.adminRequest(http.MethodPost, "/admin/v1/products/"+productID+"/variants", token,
		map[string]any{"title": storefrontVariantTitle})
	require.Equal(t, http.StatusCreated, status, "the variant could not be opened; body: %s", body)

	variantID := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, body).ID
	require.NotEmpty(t, variantID, "the variant must return an id; body: %s", body)

	return variantID
}

// bindStorefrontPrice opens a price set and binds it to the variant.
func bindStorefrontPrice(t *testing.T, s *proc, token, variantID string) {
	t.Helper()

	status, body := s.adminRequest(http.MethodPost, "/admin/v1/price-sets", token, map[string]any{
		"prices": []map[string]any{
			{"currency_code": storefrontCurrency, "amount": storefrontUnitPrice, "min_quantity": 1},
		},
	})
	require.Equal(t, http.StatusCreated, status, "the price set could not be opened; body: %s", body)

	priceSetID := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, body).ID
	require.NotEmpty(t, priceSetID, "the price set must return an id; body: %s", body)

	status, body = s.adminRequest(http.MethodPut, "/admin/v1/variants/"+variantID+"/price-set", token,
		map[string]any{"price_set_id": priceSetID})
	require.Equal(t, http.StatusOK, status,
		"the variant could not be bound to the price set; without the binding the workflow cannot find the price. body: %s", body)
}

// bindStorefrontStock opens a location and an inventory item, binds the item to
// the variant and writes the physical quantity at the location.
func bindStorefrontStock(t *testing.T, s *proc, token, variantID string) {
	t.Helper()

	status, body := s.adminRequest(http.MethodPost, "/admin/v1/stock-locations", token,
		map[string]any{"name": "Smoke Storefront Warehouse"})
	require.Equal(t, http.StatusCreated, status, "the stock location could not be opened; body: %s", body)

	locationID := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, body).ID
	require.NotEmpty(t, locationID, "the location must return an id; body: %s", body)

	status, body = s.adminRequest(http.MethodPost, "/admin/v1/inventory-items", token,
		map[string]any{"sku": "SMOKE-STOREFRONT-1", "title": storefrontVariantTitle})
	require.Equal(t, http.StatusCreated, status, "the inventory item could not be opened; body: %s", body)

	inventoryItemID := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, body).ID
	require.NotEmpty(t, inventoryItemID, "the inventory item must return an id; body: %s", body)

	status, body = s.adminRequest(http.MethodPut, "/admin/v1/variants/"+variantID+"/inventory-item",
		token, map[string]any{"inventory_item_id": inventoryItemID})
	require.Equal(t, http.StatusOK, status,
		"the variant could not be bound to the inventory item; without the binding the workflow counts the variant out of stock. body: %s",
		body)

	status, body = s.adminRequest(http.MethodPost, "/admin/v1/inventory-items/"+inventoryItemID+"/levels",
		token, map[string]any{"location_id": locationID, "stocked_quantity": storefrontStock})
	require.Equal(t, http.StatusOK, status, "the stock level could not be written; body: %s", body)
}

// openStorefrontCart opens a GUEST cart from the storefront endpoint and returns
// its id.
//
// The cart has no customer and that is deliberate: guest shopping is the
// storefront's default path, and a customer record would have added two more
// requests without adding anything to the chain the scenario tests. The contact
// address is given all the same — the order copies it from the cart, and the
// scenario verifies that the copy was made.
func openStorefrontCart(t *testing.T, s *proc, key, regionID, email string) string {
	t.Helper()

	// The body carries NEITHER the region NOR the currency: the server derives
	// both FROM THE COUNTRY. Sending them would get a 422 — the same rule as for
	// price authority (see CHANGELOG 0.5.0, the breaking change that removed
	// region_id from the POST /store/v1/carts body and made country_code
	// mandatory in its place).
	status, body := s.storefrontRequest(http.MethodPost, "/store/v1/carts", key, map[string]any{
		"country_code": storefrontCountries[0],
		"email":        email,
	})
	require.Equal(t, http.StatusCreated, status,
		"the cart could not be opened; a 404 shows the storefront endpoints are not "+
			"mounted, a 422 that the body carries a field the server determines. body: %s", body)

	cart := zarfVerisi[storefrontCart](t, body)
	require.NotEmpty(t, cart.ID, "the cart must return an id; body: %s", body)
	require.Equal(t, regionID, cart.RegionID,
		"the cart must be opened in THE COUNTRY'S region; a divergence shows the "+
			"server resolved the country to another region. body: %s", body)

	return cart.ID
}
