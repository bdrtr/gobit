//go:build integration

package e2e

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file proves, with the REAL stack, the act ADR 0146 opened: an operator on
// the telephone builds a cart, and the shopper pays for it on the storefront.
//
// # Why the proof has to be here and not in the handler's own tests
//
// The handler's job is one line — it writes the channel the request names into
// the principal — and a unit test with a fake flow says that line ran. What it
// cannot say is that the claim REACHES the catalog: a fake answers the same for
// every channel, so a handler that wrote the field into a copy nobody reads, or
// a flow that stopped consulting the identity, would leave every unit test
// green. The assertion that costs something is a variant that exists, is
// published, is priced, and is still refused because it belongs to another
// shopfront.
//
// # Why the first test's last step goes back to the storefront
//
// Because that was ADR 0146's decision: the admin surface opened a cart and put
// priced lines in it, and the money was taken through the endpoint the shopper's
// own session uses, with the totals the server computed in front of them. ADR
// 0286 added the operator's own completion for an order paid later through an
// offline method, with the total the operator read to the customer;
// [TestAnOperatorCompletesATelephoneOrderPaidLater] walks it.

const (
	// adminCartUnitPrice is the price the catalog holds for the telephone
	// order's variant.
	adminCartUnitPrice int64 = 25_000
	// adminCartQuantity is how many units the operator types in.
	adminCartQuantity int64 = 2
	// adminCartSubtotal, adminCartTax and adminCartTotal are computed by hand
	// from the two figures above and the taxed region's rate (20%), so the
	// assertion is against arithmetic rather than against whatever the server
	// happened to answer.
	adminCartSubtotal int64 = 50_000
	adminCartTax      int64 = 10_000
	adminCartTotal    int64 = 60_000
	// adminCartStock is the physical quantity the fixture puts on the shelf.
	adminCartStock int64 = 4
)

// adminCartRequest makes an admin request with the fully privileged secret key.
//
// It goes through the production guard stack, which matters more here than
// anywhere else in this file: the endpoint's whole behavior is what it does to
// the identity that stack produced.
func adminCartRequest(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	request.Header.Set("Authorization", "Bearer "+secretKey)
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	testRouter.ServeHTTP(recorder, request)

	return recorder
}

// openAdminCart opens a cart from the admin surface.
//
// It names no channel, so the cart is priced in none (ADR 0397); the channel
// that scopes the catalog is claimed on each line write instead.
func openAdminCart(t *testing.T, customerID, email string) *httptest.ResponseRecorder {
	t.Helper()

	return adminCartRequest(t, http.MethodPost, "/admin/v1/carts",
		fmt.Sprintf(`{"country_code":%q,"customer_id":%q,"email":%q}`,
			taxedCountry, customerID, email))
}

// openAdminCartID opens a guest cart from the admin surface and returns its id.
func openAdminCartID(t *testing.T) string {
	t.Helper()

	opened := openAdminCart(t, "", "")
	require.Equal(t, http.StatusCreated, opened.Code, "body: %s", opened.Body.String())

	cartID, ok := storefrontData(t, opened)["id"].(string)
	require.True(t, ok, "the opened cart must carry an identity; body: %s", opened.Body.String())

	return cartID
}

// addAdminLine adds a line from the admin surface under the named channel.
func addAdminLine(t *testing.T, cartID, channelID, variantID string, quantity int64) *httptest.ResponseRecorder {
	t.Helper()

	return adminCartRequest(t, http.MethodPost, "/admin/v1/carts/"+cartID+"/line-items",
		fmt.Sprintf(`{"sales_channel_id":%q,"variant_id":%q,"quantity":%d}`,
			channelID, variantID, quantity))
}

// TestAnOperatorBuildsACartTheShopperPaysFor is the whole act, end to end.
//
// Open a cart for a named customer, put a priced line in it from the admin
// surface, then hand the cart's id to the storefront and let the shopper
// complete it. What it proves beyond the individual endpoints is that the two
// surfaces are talking about the SAME cart: the operator's lines are the ones
// the shopper pays for, at the server's price, and the order that comes out
// belongs to the customer whose name the operator typed.
func TestAnOperatorBuildsACartTheShopperPaysFor(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	const variantTitle = "E2E Telephone Order"
	variantID, stockItemID := newStockedVariant(ctx, t, variantTitle, map[string]int64{
		taxedCurrency: adminCartUnitPrice,
	}, adminCartStock)

	opened := openAdminCart(t, customerID, email)
	require.Equal(t, http.StatusCreated, opened.Code, "body: %s", opened.Body.String())

	cart := storefrontData(t, opened)
	cartID, ok := cart["id"].(string)
	require.True(t, ok, "the opened cart must carry an identity; body: %s", opened.Body.String())
	assert.Equal(t, customerID, cart["customer_id"],
		"the cart must belong to the customer the operator named; that is the point of "+
			"the endpoint — a guest cart would carry neither their history nor their "+
			"company's spending limit")
	assert.Equal(t, taxedCurrency, cart["currency_code"],
		"the currency must be derived from the country ON THE SERVER; the body sends none")

	added := addAdminLine(t, cartID, testChannelID, variantID, adminCartQuantity)
	require.Equal(t, http.StatusCreated, added.Code, "body: %s", added.Body.String())

	line := storefrontData(t, added)
	assert.InDelta(t, float64(adminCartUnitPrice), line["unit_price"], 0,
		"the unit price must come FROM THE CATALOG; the operator sent no price at all")
	assert.Equal(t, variantTitle, line["title"],
		"the title must be copied from the catalog too")

	// The shopper reads the cart on their OWN surface, with their own key. The
	// totals they see are the ones the operator's writes produced.
	fetched := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
	require.Equal(t, http.StatusOK, fetched.Code, "body: %s", fetched.Body.String())

	shown := storefrontData(t, fetched)
	assert.InDelta(t, float64(adminCartSubtotal), shown["subtotal"], 0)
	assert.InDelta(t, float64(adminCartTax), shown["tax_total"], 0,
		"the tax must be computed with the region's rate; had the calculation pass not "+
			"run it would have stayed zero")
	assert.InDelta(t, float64(adminCartTotal), shown["total"], 0)

	require.Equal(t, adminCartStock, sellableQuantity(ctx, t, stockItemID),
		"a cart built by an operator must not reserve stock either")

	// The money is taken on the storefront, by the shopper, against the total
	// they were shown.
	done := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, adminCartTotal))
	require.Equal(t, http.StatusOK, done.Code, "body: %s", done.Body.String())

	result := storefrontData(t, done)
	orderID, _ := result["order_id"].(string)
	require.NotEmpty(t, orderID, "the response must carry the order's id")
	assert.InDelta(t, float64(adminCartTotal), result["total"], 0,
		"the captured amount must be the hand-computed grand total")

	assert.Equal(t, adminCartStock-adminCartQuantity, sellableQuantity(ctx, t, stockItemID),
		"the capture must move the stock the operator's line asked for")

	// The operator opened the cart and the shopper placed the order, so the
	// order names no operator: the cart's opener is not its placer (ADR 0298).
	order, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	assert.Empty(t, order.PlacedBy)
}

// TestAnOperatorCompletesATelephoneOrderPaidLater is ADR 0286 end to end: the
// operator opens a cart for a customer, adds a line, writes the address and
// the shipping method, reads the total to the customer and completes the cart
// with a bank transfer. A provider the checkout would capture is refused
// first, and the order that comes out owes its total with its stock deducted.
func TestAnOperatorCompletesATelephoneOrderPaidLater(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, stockItemID := newStockedVariant(ctx, t, "E2E Telephone Order Paid Later", map[string]int64{
		taxedCurrency: adminCartUnitPrice,
	}, adminCartStock)
	optionID := newShippingOption(ctx, t, newShippingProfile(ctx, t, "Telephone profile"),
		"Telephone delivery", 4_900, false)

	opened := openAdminCart(t, customerID, email)
	require.Equal(t, http.StatusCreated, opened.Code, "body: %s", opened.Body.String())
	cartID, ok := storefrontData(t, opened)["id"].(string)
	require.True(t, ok, opened.Body.String())
	added := addAdminLine(t, cartID, testChannelID, variantID, adminCartQuantity)
	require.Equal(t, http.StatusCreated, added.Code, "body: %s", added.Body.String())

	addressed := adminCartRequest(t, http.MethodPut, "/admin/v1/carts/"+cartID+"/shipping-address",
		fmt.Sprintf(`{"first_name":"Tele","last_name":"Phone","address_1":"Street 1",`+
			`"city":"City","postal_code":"00000","country_code":%q}`, taxedCountry))
	require.Equal(t, http.StatusOK, addressed.Code, "body: %s", addressed.Body.String())
	shipped := adminCartRequest(t, http.MethodPost, "/admin/v1/carts/"+cartID+"/shipping-methods",
		fmt.Sprintf(`{"shipping_option_id":%q}`, optionID))
	require.Equal(t, http.StatusCreated, shipped.Code, "body: %s", shipped.Body.String())

	// The total the operator reads to the customer is the cart's own, repriced
	// after each write.
	read := adminCartRequest(t, http.MethodGet, "/admin/v1/carts/"+cartID, "")
	require.Equal(t, http.StatusOK, read.Code, "body: %s", read.Body.String())
	total, ok := storefrontData(t, read)["total"].(float64)
	require.True(t, ok, read.Body.String())
	require.Greater(t, int64(total), adminCartTotal, "the shipping is in the total")

	byCard := adminCartRequest(t, http.MethodPost, "/admin/v1/carts/"+cartID+"/complete",
		fmt.Sprintf(`{"sales_channel_id":%q,"payment_provider_id":"manual","expected_total":%d}`,
			testChannelID, int64(total)))
	require.Equal(t, http.StatusUnprocessableEntity, byCard.Code, "body: %s", byCard.Body.String())
	assert.Contains(t, byCard.Body.String(), "checkout_workflow_offline_method_required")

	// Zero is a storefront's "do not compare"; the operator's completion
	// compares it like any other figure (D199).
	unread := adminCartRequest(t, http.MethodPost, "/admin/v1/carts/"+cartID+"/complete",
		fmt.Sprintf(`{"sales_channel_id":%q,"payment_provider_id":%q,"expected_total":0}`,
			testChannelID, offlineMethod))
	require.Equal(t, http.StatusConflict, unread.Code, "body: %s", unread.Body.String())
	assert.Contains(t, unread.Body.String(), "checkout_workflow_total_mismatch")

	done := adminCartRequest(t, http.MethodPost, "/admin/v1/carts/"+cartID+"/complete",
		fmt.Sprintf(`{"sales_channel_id":%q,"payment_provider_id":%q,"expected_total":%d}`,
			testChannelID, offlineMethod, int64(total)))
	require.Equal(t, http.StatusOK, done.Code, "body: %s", done.Body.String())
	result := storefrontData(t, done)
	assert.InDelta(t, total, result["total"], 0)
	assert.InDelta(t, total, result["outstanding"], 0, "the order owes its whole total")
	orderID, _ := result["order_id"].(string)
	require.NotEmpty(t, orderID)

	order, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	assert.Equal(t, customerID, order.CustomerID, "the order is the customer's the operator named")
	assert.Zero(t, order.Summary.PaidTotal)
	assert.Equal(t, adminCartStock-adminCartQuantity, sellableQuantity(ctx, t, stockItemID),
		"the order's stock is deducted")

	// The order is among the customer's orders awaiting their payment, and
	// not among the others (ADR 0294).
	for awaiting, listed := range map[string]bool{"true": true, "false": false} {
		rec := adminCartRequest(t, http.MethodGet,
			"/admin/v1/orders?customer_id="+customerID+"&awaiting_payment="+awaiting, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, listed, strings.Contains(rec.Body.String(), orderID), "awaiting_payment=%s", awaiting)
	}

	// The order names the key that placed it, on the admin record and in the
	// operator filter (ADR 0298).
	assert.NotEmpty(t, order.PlacedBy, "the order names the operator's identity")
	detail := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+orderID, "")
	require.Equal(t, http.StatusOK, detail.Code, detail.Body.String())
	assert.Equal(t, order.PlacedBy, storefrontData(t, detail)["placed_by"])
	for placed, listed := range map[string]bool{"true": true, "false": false} {
		rec := adminCartRequest(t, http.MethodGet,
			"/admin/v1/orders?customer_id="+customerID+"&placed_by_operator="+placed, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, listed, strings.Contains(rec.Body.String(), orderID), "placed_by_operator=%s", placed)
	}
}

// TestAnOperatorsLineIsPricedInTheChannelItNames verifies that the claim in the
// body really reaches the catalog.
//
// This is the assertion the handler's own tests cannot make. The variant refused
// below exists, is published and is priced; the only thing wrong with it is the
// shopfront it belongs to. And the same variant is accepted when the operator
// names ITS channel, which is what says the refusal is about the channel rather
// than about the variant.
func TestAnOperatorsLineIsPricedInTheChannelItNames(t *testing.T) {
	ground := channelCartFixture(t)
	catalog := channelCatalogFixture(t)

	firstCart := openAdminCartID(t)

	rejected := addAdminLine(t, firstCart, testChannelID, ground.secondChannelVariant, 1)
	assert.Equal(t, http.StatusNotFound, rejected.Code,
		"another shopfront's variant must not enter the operator's cart; body: %s",
		rejected.Body.String())

	accepted := addAdminLine(t, firstCart, testChannelID, ground.firstChannelVariant, 1)
	assert.Equal(t, http.StatusCreated, accepted.Code,
		"the named channel's own variant must enter; body: %s", accepted.Body.String())

	// The reverse direction, on the same fixture: the rule is not a one-way
	// barrier but the two shopfronts being closed to each other's catalog, and
	// an operator naming the second channel gets the second channel's answers.
	secondCart := openAdminCartID(t)

	reverseRejected := addAdminLine(t, secondCart, catalog.secondChannelID, ground.firstChannelVariant, 1)
	assert.Equal(t, http.StatusNotFound, reverseRejected.Code,
		"the first shopfront's variant must not enter a second-channel cart; body: %s",
		reverseRejected.Body.String())

	reverseAccepted := addAdminLine(t, secondCart, catalog.secondChannelID, ground.secondChannelVariant, 1)
	assert.Equal(t, http.StatusCreated, reverseAccepted.Code,
		"the second channel's own variant must enter; body: %s", reverseAccepted.Body.String())
}

// TestTheChannelIsClaimedPerRequestAndNotRemembered verifies that the cart does
// not carry the channel it was opened under.
//
// The claim is the operator's and it is made per request, the same way the
// storefront's key carries it on every request. What that costs is visible here:
// a line written under another channel is accepted into a cart opened under the
// first one. It is the operator's own doing, they hold the write scope, and the
// audit ring recorded them — and it is written down rather than discovered.
func TestTheChannelIsClaimedPerRequestAndNotRemembered(t *testing.T) {
	ground := channelCartFixture(t)
	catalog := channelCatalogFixture(t)

	cartID := openAdminCartID(t)

	first := addAdminLine(t, cartID, testChannelID, ground.firstChannelVariant, 1)
	require.Equal(t, http.StatusCreated, first.Code, "body: %s", first.Body.String())

	crossed := addAdminLine(t, cartID, catalog.secondChannelID, ground.secondChannelVariant, 1)
	assert.Equal(t, http.StatusCreated, crossed.Code,
		"the claim on the WRITE decides the catalog, not the one the cart was opened "+
			"with; body: %s", crossed.Body.String())
}

// TestAnOperatorMustNameTheChannelToPriceALine verifies that a line write making
// no claim is refused, and refused BEFORE anything is written.
//
// The alternative was to serve it with the admin key's own channels, which are
// none. A principal with no channels is bound to no channel rather than
// unscoped, so the operator would have got 404 for every product the shop had
// assigned anywhere — with the same message a mistyped variant id returns.
func TestAnOperatorMustNameTheChannelToPriceALine(t *testing.T) {
	ground := channelCartFixture(t)

	cartID := openAdminCartID(t)

	unclaimedLine := adminCartRequest(t, http.MethodPost, "/admin/v1/carts/"+cartID+"/line-items",
		fmt.Sprintf(`{"variant_id":%q,"quantity":1}`, ground.unassignedVariant))
	assert.Equal(t, http.StatusUnprocessableEntity, unclaimedLine.Code,
		"a line cannot be priced without a shopfront; body: %s", unclaimedLine.Body.String())

	// Nothing was written: the refusal is not a rollback but a request that never
	// reached the flow.
	fetched := adminCartRequest(t, http.MethodGet, "/admin/v1/carts/"+cartID, "")
	require.Equal(t, http.StatusOK, fetched.Code, "body: %s", fetched.Body.String())
	assert.Empty(t, storefrontData(t, fetched)["items"],
		"the refused line must not be in the cart")
}

// TestAnUnassignedVariantEntersAnOperatorsCartUnderAnyChannel verifies the
// backward compatible half of the rule on this surface too.
//
// A shop that assigns nothing to channels is the common case, and the operator
// naming a channel must not make its whole catalog invisible.
func TestAnUnassignedVariantEntersAnOperatorsCartUnderAnyChannel(t *testing.T) {
	ground := channelCartFixture(t)
	catalog := channelCatalogFixture(t)

	for name, channelID := range map[string]string{
		"first shopfront":  testChannelID,
		"second shopfront": catalog.secondChannelID,
	} {
		t.Run(name, func(t *testing.T) {
			cartID := openAdminCartID(t)

			added := addAdminLine(t, cartID, channelID, ground.unassignedVariant, 1)
			assert.Equal(t, http.StatusCreated, added.Code,
				"a product assigned to no channel must be sellable from every shopfront; body: %s",
				added.Body.String())
		})
	}
}
