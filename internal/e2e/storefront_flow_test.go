//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	b2bmodels "github.com/bdrtr/gobit/internal/modules/b2b/models"
	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
)

// This file proves that the workflows are wired into the PRODUCTION BINARY.
//
// # Why the other e2e tests were not enough
//
// Every scenario in phases 5-7 called the workflows DIRECTLY
// (workflows.AddLineItem, orderWorkflows.CompleteCart). That proves the
// workflows compute correctly, but it also stays green in a setup where NOBODY
// calls them — and that is exactly how it was: cmd/server registered only the
// saga ENGINE, and there was not a single line of production code calling the
// workflows themselves. In the running binary there was NO path turning a cart
// into an order; payment, shipping, the checkout promotion, the order.placed
// notification and the b2b spending limit were UNREACHABLE.
//
// The scenarios here therefore never touch a workflow: every step goes in
// through the HTTP endpoint, with a publishable key, through the production
// guard stack. The day the registration line is deleted (or the module's name
// constant drifts) these tests fail; the tests calling the workflows directly
// would not have.
//
// # Second proof: PRICE AUTHORITY
//
// The POST /store/v1/carts/{id}/line-items body accepted "unit_price" and the
// cart service wrote it AS IT CAME; only the range was checked, its correctness
// was not. The storefront's identity is the publishable key and it lives in the
// browser — so this was a "write your own price" endpoint everyone could reach.
// [TestStorefrontRejectsClientPrice] shows that door is now closed, and the
// happy-path test shows the price really does come from the catalog.

// The MANUALLY computed amounts of the storefront scenario.
//
// The region is taxed at 20% (2000 basis points) and no shipping method is
// selected:
//
//	32_000 × 2 = 64_000 subtotal
//	64_000 × 20% = 12_800 tax
//	64_000 - 0 + 12_800 + 0 = 76_800 grand total
const (
	storefrontUnitPrice    int64 = 32_000
	storefrontQuantity     int64 = 2
	storefrontSubtotal     int64 = 64_000
	storefrontTax          int64 = 12_800
	storefrontTotal        int64 = 76_800
	storefrontInitialStock int64 = 5
	// storefrontRemainingStock is the physical quantity expected after capture: 5 - 2.
	storefrontRemainingStock int64 = 3
)

// TestStorefrontCartBecomesOrder proves that a cart turns into an order by
// passing through the PRODUCTION ENDPOINTS.
//
// The chain is HTTP end to end: open a cart -> add a line (priceless) -> read
// the cart -> complete. The outcome is then verified from the modules' OWN
// data — was an order really opened, was money captured, did stock drop, was
// the cart closed, was "order.placed" published.
func TestStorefrontCartBecomesOrder(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	const variantTitle = "E2E Storefront Product"
	variantID, stockItemID := newStockedVariant(ctx, t, variantTitle, map[string]int64{
		taxedCurrency: storefrontUnitPrice,
	}, storefrontInitialStock)

	cartID := openStorefrontCart(t, customerID, email)

	// --- line: NO price in the body, the server decides the price ---

	added := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items",
		fmt.Sprintf(`{"variant_id":%q,"quantity":%d}`, variantID, storefrontQuantity))
	require.Equal(t, http.StatusCreated, added.Code, "body: %s", added.Body.String())

	line := storefrontData(t, added)
	assert.InDelta(t, float64(storefrontUnitPrice), line["unit_price"], 0,
		"the unit price must come FROM THE CATALOG; the client sent no price at all")
	assert.Equal(t, variantTitle, line["title"],
		"the title must be copied from the catalog too; the client sends no title either")
	assert.InDelta(t, float64(storefrontSubtotal), line["subtotal"], 0,
		"the line subtotal must be written during the calculation pass")

	// --- cart: read over HTTP the totals must be computed and FRESH ---

	fetched := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
	require.Equal(t, http.StatusOK, fetched.Code, "body: %s", fetched.Body.String())

	cart := storefrontData(t, fetched)
	assert.InDelta(t, float64(storefrontSubtotal), cart["subtotal"], 0)
	assert.InDelta(t, float64(storefrontTax), cart["tax_total"], 0,
		"the tax must be computed with the region's rate; had the calculation pass "+
			"not run it would have stayed zero")
	assert.InDelta(t, float64(storefrontTotal), cart["total"], 0)
	assert.Equal(t, false, cart["totals_stale"],
		"totals must be FRESH once a line has been added; a stale total cannot be ordered")

	require.Equal(t, storefrontInitialStock, sellableQuantity(ctx, t, stockItemID),
		"adding a line to a cart must NOT reserve stock")

	// --- completion ---

	done := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, storefrontTotal))
	require.Equal(t, http.StatusOK, done.Code, "body: %s", done.Body.String())

	result := storefrontData(t, done)
	orderID, _ := result["order_id"].(string)
	require.NotEmpty(t, orderID, "the response must carry the order's id")
	assert.Equal(t, cartID, result["cart_id"])
	assert.Equal(t, taxedCurrency, result["currency_code"])
	assert.InDelta(t, float64(storefrontTotal), result["total"], 0,
		"the captured amount must be the MANUALLY computed grand total")

	// The response carries no INTERNAL ids: the payment session, collection and
	// reservation ids are internal structure the store client could not use from
	// any endpoint.
	for _, field := range []string{
		"payment_id", "payment_session_id", "payment_collection_id",
		"reservation_ids", "warnings",
	} {
		assert.NotContains(t, result, field,
			"%s must not appear in the storefront response", field)
	}

	// --- the outcome is verified from the modules' OWN data ---

	order, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err, "the resulting order must be readable from the order module")
	assert.Equal(t, ordermodels.OrderPending, order.Status)
	assert.Equal(t, cartID, order.CartID, "the order must document the cart it was born from")
	assert.Equal(t, customerID, order.CustomerID)
	assert.Equal(t, storefrontSubtotal, order.Subtotal,
		"the order's subtotal must be the SAME as the cart's")
	assert.Equal(t, storefrontTax, order.TaxTotal, "the order's tax must be the SAME as the cart's")
	assert.Equal(t, storefrontTotal, order.Total,
		"the order's grand total must be the same as the cart's and as the captured amount")
	assert.Equal(t, email, order.Email,
		"the contact address must come FROM THE CART; the completion body carries no "+
			"e-mail address and cannot — the cart is the address's only source")

	require.Len(t, order.Items, 1, "the cart's single line must pass into the order as a single line")
	assert.Equal(t, storefrontUnitPrice, order.Items[0].UnitPrice,
		"the unit price on the order must also be the price coming from the catalog; "+
			"there was no field the client could have sent at any step")
	assert.Equal(t, variantTitle, order.Items[0].Title,
		"the line title must be copied from the catalog")

	closed, err := cartSvc.GetCart(ctx, cartID)
	require.NoError(t, err)
	assert.True(t, closed.Completed(),
		"the cart must be closed; if it did not close, the same cart would be the source "+
			"of a second order")

	assert.Equal(t, storefrontRemainingStock, sellableQuantity(ctx, t, stockItemID),
		"stock must drop physically after capture")

	event := eventLog.waitFor(t, orderID)
	assert.Equal(t, orderID, event.Data["order_id"],
		"the order.placed event must be published; an order placed from the storefront "+
			"must produce a notification too")
}

// TestStorefrontRejectsClientPrice proves at the real endpoint that the
// storefront DOES NOT ACCEPT a price or a title.
//
// The claim has two layers: the request must be rejected AND no line may be
// written to the cart. Looking at the status code alone would not have been
// enough — had the field been silently ignored and the line still added, the
// client would believe what it sent while the server wrote a different price.
func TestStorefrontRejectsClientPrice(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Price Authority Product", map[string]int64{
		taxedCurrency: storefrontUnitPrice,
	}, storefrontInitialStock)

	cartID := openStorefrontCart(t, customerID, email)

	for name, body := range map[string]string{
		"penny price":   fmt.Sprintf(`{"variant_id":%q,"quantity":1,"unit_price":1}`, variantID),
		"zero price":    fmt.Sprintf(`{"variant_id":%q,"quantity":1,"unit_price":0}`, variantID),
		"made-up title": fmt.Sprintf(`{"variant_id":%q,"quantity":1,"title":"Free"}`, variantID),
	} {
		t.Run(name, func(t *testing.T) {
			rec := storefrontRequest(t, http.MethodPost,
				"/store/v1/carts/"+cartID+"/line-items", body)

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code,
				"the storefront must not accept a price/title; body: %s", rec.Body.String())
		})
	}

	detail, err := cartSvc.GetCart(ctx, cartID)
	require.NoError(t, err)
	assert.Empty(t, detail.Items,
		"none of the rejected requests may write a line to the cart")
}

// TestStorefrontRejectsClientRegion proves at the real endpoint that the cart
// OPENING endpoint accepts neither a region nor a currency.
//
// The class is the same as [TestStorefrontRejectsClientPrice]: a value decided
// by the client was entering the server's decision.
//
//   - currency_code selected WHICH PRICE LIST would be applied and the
//     divergence was not rejected: the cart was opened in the currency the
//     client named even when its region was TRY, and the line was priced from
//     that list too.
//   - region_id selected the cart's TAX RATE and was not what the customer
//     wanted to express in the first place: a customer picks a country, and the
//     region is that country's counterpart on the server.
//
// The claim has two layers: the request must be rejected AND no cart may be
// written. Had the field been silently ignored the client would believe what it
// sent while the server opened a cart in a different region.
func TestStorefrontRejectsClientRegion(t *testing.T) {
	ctx := t.Context()

	for name, body := range map[string]string{
		// The currency in the body is DIFFERENT from the region's: this exact
		// request used to open a cart in the TRY region with the EUR price list.
		"currency": fmt.Sprintf(`{"country_code":%q,"currency_code":%q}`,
			taxedCountry, untaxedCurrency),
		"region": fmt.Sprintf(`{"country_code":%q,"region_id":%q}`,
			taxedCountry, untaxedRegionID),
	} {
		t.Run(name, func(t *testing.T) {
			priorCount := cartCount(ctx, t)

			rejected := storefrontRequest(t, http.MethodPost, "/store/v1/carts", body)

			assert.Equal(t, http.StatusUnprocessableEntity, rejected.Code,
				"the storefront must not accept a field the server derives; body: %s",
				rejected.Body.String())
			assert.Equal(t, priorCount, cartCount(ctx, t),
				"a rejected request must NOT write a cart; if it did, the client would "+
					"believe the value it sent had been applied")
		})
	}
}

// TestStorefrontUnknownCountryOpensNoCart proves that a country sales were
// never opened for DOES NOT let a cart be opened.
//
// Because the region no longer comes from the body, the question "does a region
// exist" turned into "do we sell to this country", and its answer is 404: it is
// a situation the client can fix (pick another country), not a server failure.
// If the FORM of the country code is broken the answer is 422 — the two are
// separate questions and must stay separate.
func TestStorefrontUnknownCountryOpensNoCart(t *testing.T) {
	ctx := t.Context()

	for name, scenario := range map[string]struct {
		body   string
		status int
		reason string
	}{
		"country without a region": {
			body: `{"country_code":"NL"}`, status: http.StatusNotFound,
			reason: "the operator has not opened sales to that country; the client picks another",
		},
		"malformed country code": {
			body: `{"country_code":"TURKIYE"}`, status: http.StatusUnprocessableEntity,
			reason: "a code that is not ISO 3166-1 alpha-2 is a body error",
		},
		"empty country code": {
			body: `{}`, status: http.StatusUnprocessableEntity,
			reason: "the country is mandatory; there is no other source to derive the region from",
		},
	} {
		t.Run(name, func(t *testing.T) {
			priorCount := cartCount(ctx, t)

			rejected := storefrontRequest(t, http.MethodPost, "/store/v1/carts", scenario.body)

			assert.Equal(t, scenario.status, rejected.Code,
				"%s; body: %s", scenario.reason, rejected.Body.String())
			assert.Equal(t, priorCount, cartCount(ctx, t),
				"a cart whose region cannot be derived must NOT be opened at all")
		})
	}
}

// TestStorefrontCurrencyDerivedFromRegion proves that a cart's currency comes
// FROM THE REGION and that the price really is selected from that currency's
// list.
//
// The two regions carry DIFFERENT currencies and a single variant is priced in
// both. Looking only at the cart's currency_code field would not have been
// enough: had the field been written correctly while the price was read from
// another list, the claim would still have passed. The real proof is that the
// same variant takes a DIFFERENT unit price in the two carts — the currency is
// not a label on the cart, it is the price's SELECTOR.
func TestStorefrontCurrencyDerivedFromRegion(t *testing.T) {
	ctx := t.Context()

	// The amounts are deliberately NOT close to one another: should something
	// drift, a single number should reveal which list was read.
	const (
		taxedRegionPrice   int64 = 30_000
		untaxedRegionPrice int64 = 1_100
	)

	customerID, email := newCustomer(ctx, t)
	variantID := newVariant(ctx, t, "E2E Currency Product", map[string]int64{
		taxedCurrency:   taxedRegionPrice,
		untaxedCurrency: untaxedRegionPrice,
	})

	for _, scenario := range []struct {
		name     string
		country  string
		currency string
		price    int64
	}{
		{"taxed region", taxedCountry, taxedCurrency, taxedRegionPrice},
		{"untaxed region", untaxedCountry, untaxedCurrency, untaxedRegionPrice},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			cartID := openStorefrontCartInCountry(t, scenario.country, customerID, email)

			fetched := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
			require.Equal(t, http.StatusOK, fetched.Code, "body: %s", fetched.Body.String())
			assert.Equal(t, scenario.currency, storefrontData(t, fetched)["currency_code"],
				"the cart's currency must be THE COUNTRY'S REGION'S; the client sent "+
					"neither a region nor a currency")

			added := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items",
				fmt.Sprintf(`{"variant_id":%q,"quantity":1}`, variantID))
			require.Equal(t, http.StatusCreated, added.Code, "body: %s", added.Body.String())

			assert.InDelta(t, float64(scenario.price), storefrontData(t, added)["unit_price"], 0,
				"the unit price must be selected from the list in the cart's currency; had "+
					"the wrong list been read the customer would pay another country's price")
		})
	}
}

// TestStorefrontPlacesNoOrderOnUnapprovedTotal proves that when the total the
// customer saw and the total the server computes diverge there is NO side
// effect whatsoever.
//
// This is the guard against the payment step's most expensive mistake: the
// totals are REFRESHED at the start of the completion workflow, meaning a price
// that changed in the catalog could have led to an amount different from the
// one the customer approved being captured. Because the check runs BEFORE the
// saga's first step, no stock is reserved and no order is opened either — and
// the cart is still in a completable state.
func TestStorefrontPlacesNoOrderOnUnapprovedTotal(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, stockItemID := newStockedVariant(ctx, t, "E2E Approved Total Product",
		map[string]int64{taxedCurrency: storefrontUnitPrice}, storefrontInitialStock)

	cartID := openStorefrontCart(t, customerID, email)
	added := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items",
		fmt.Sprintf(`{"variant_id":%q,"quantity":%d}`, variantID, storefrontQuantity))
	require.Equal(t, http.StatusCreated, added.Code, "body: %s", added.Body.String())

	// The approved total is one minor unit short: the customer saw ANOTHER amount.
	conflict := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, storefrontTotal-1))
	require.Equal(t, http.StatusConflict, conflict.Code,
		"a diverging total must be a 409; on a 500 the client would retry, whereas "+
			"what it should do is have the customer approve the new amount. body: %s",
		conflict.Body.String())

	assert.Equal(t, storefrontInitialStock, sellableQuantity(ctx, t, stockItemID),
		"a rejected completion must NOT reserve stock")

	open, err := cartSvc.GetCart(ctx, cartID)
	require.NoError(t, err)
	assert.False(t, open.Completed(), "a rejected completion must not close the cart")

	// When the approved total is given CORRECTLY the same cart must be completable:
	// a rejected attempt must NOT burn the workflow's idempotency key.
	done := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, storefrontTotal))
	require.Equal(t, http.StatusOK, done.Code, "body: %s", done.Body.String())
	assert.NotEmpty(t, storefrontData(t, done)["order_id"])
}

// TestStorefrontExpectedTotalIsMandatory verifies that the guard CANNOT BE
// TURNED OFF by the client.
//
// Had the field been optional, every client that forgot to send it would have
// silently disabled the guard; this repository's recurring bug class is exactly
// that — the rule is defined, there is no place where it is enforced.
func TestStorefrontExpectedTotalIsMandatory(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Mandatory Approval Product",
		map[string]int64{taxedCurrency: storefrontUnitPrice}, storefrontInitialStock)

	cartID := openStorefrontCart(t, customerID, email)
	added := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items",
		fmt.Sprintf(`{"variant_id":%q,"quantity":%d}`, variantID, storefrontQuantity))
	require.Equal(t, http.StatusCreated, added.Code, "body: %s", added.Body.String())

	missing := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		fmt.Sprintf(`{"payment_provider_id":%q}`, manual.ID))
	assert.Equal(t, http.StatusUnprocessableEntity, missing.Code,
		"no order may be placed without the approved total being declared; body: %s",
		missing.Body.String())

	open, err := cartSvc.GetCart(ctx, cartID)
	require.NoError(t, err)
	assert.False(t, open.Completed())
}

// TestStorefrontB2BLimitRejectionReportsReason proves that the REASON for the
// rejection reaches the storefront.
//
// # Why the status code is not enough
//
// A purchase exceeding the spending limit was already getting a 409 and no
// money was captured either (see b2b_test.go). The only thing missing was the
// client's ability to READ it: the body's code was being filled with the saga
// engine's own constant ("workflow_step_failed") and "spending_limit" appeared
// nowhere in the response; the reason lived only in the server log.
//
// The difference is behavioural, not cosmetic. A 409 is exactly the class a
// RETRY DOES NOT SOLVE: a storefront that cannot tell them apart either tells
// the user "a temporary error, try again" and turns away, for nothing, the
// employee who needs their limit raised, or treats every 409 as permanent and
// swallows the conflicts that really were temporary. This is a variant of the
// repository's recurring bug class — the rule is enforced, the error code is
// produced, but it does not reach the consumer.
//
// The chain is HTTP end to end and no workflow is touched: that the code makes
// it all the way into the body can only be seen by going through the production
// transport layer.
func TestStorefrontB2BLimitRejectionReportsReason(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, stockItemID := newStockedVariant(ctx, t, "E2E Storefront B2B Limit",
		map[string]int64{taxedCurrency: storefrontUnitPrice}, storefrontInitialStock)

	// The limit is BELOW the cart's grand total: 1_000 < 76_800.
	limit := int64(1_000)
	b2bCalisan(ctx, t, customerID, &limit, b2bmodels.ResetNever)

	cartID := openStorefrontCart(t, customerID, email)
	added := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items",
		fmt.Sprintf(`{"variant_id":%q,"quantity":%d}`, variantID, storefrontQuantity))
	require.Equal(t, http.StatusCreated, added.Code, "body: %s", added.Body.String())

	rejected := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, storefrontTotal))

	require.Equal(t, http.StatusConflict, rejected.Code,
		"exceeding the limit is a conflict: the request is formally valid, the reason "+
			"for the rejection is the system's state AT THAT MOMENT. body: %s", rejected.Body.String())
	assert.Equal(t, ordersvc.CodeSpendingLimitExceeded, errorCode(t, rejected),
		"the body's code must name the REASON for the rejection; if the engine's own "+
			"constant shows up, the client cannot tell a limit overrun from a temporary "+
			"conflict. body: %s", rejected.Body.String())

	// Reporting the reason must not weaken the claim that the rejection is free.
	assert.Equal(t, storefrontInitialStock, sellableQuantity(ctx, t, stockItemID),
		"a rejected purchase must NOT hold stock")

	open, err := cartSvc.GetCart(ctx, cartID)
	require.NoError(t, err, "the cart of a rejected purchase must still be readable")
	assert.False(t, open.Completed(),
		"the cart of a rejected purchase must NOT close; if it did, the customer would "+
			"have to rebuild the cart once their limit was fixed")
}

// openStorefrontCart opens a cart in the TAXED country from the store endpoint
// and returns its id.
func openStorefrontCart(t *testing.T, customerID, email string) string {
	t.Helper()

	return openStorefrontCartInCountry(t, taxedCountry, customerID, email)
}

// openStorefrontCartInCountry opens a cart in the GIVEN country from the store
// endpoint and returns its id.
//
// The body carries neither a region nor a currency, and it cannot: the server
// derives both from the country. The country being a parameter is precisely
// what makes that visible — the only way to change a cart's region (and
// therefore its currency) is to pick ANOTHER country, because that is the only
// thing the customer is able to express.
func openStorefrontCartInCountry(t *testing.T, countryCode, customerID, email string) string {
	t.Helper()

	rec := storefrontRequest(t, http.MethodPost, "/store/v1/carts", fmt.Sprintf(
		`{"country_code":%q,"customer_id":%q,"email":%q}`, countryCode, customerID, email))
	require.Equal(t, http.StatusCreated, rec.Code,
		"could not open the cart; body: %s", rec.Body.String())

	return readCartID(t, rec)
}

// storefrontCompletionBody builds the body of the completion request.
//
// The payment behaviour comes from the manual provider's session data (see
// [paymentBehavior]); the saga itself has no test hook whatsoever. The body
// carries NO location and cannot: which warehouse to ship out of is a shipping
// decision and the workflow makes it per line itself.
func storefrontCompletionBody(t *testing.T, approvedTotal int64) string {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"payment_provider_id": manual.ID,
		"payment_data":        paymentBehavior(t, manual.OutcomeAuthorize),
		"expected_total":      approvedTotal,
	})
	require.NoError(t, err, "could not encode the completion body")

	return string(body)
}

// storefrontRequest makes a store request with the publishable key.
//
// The key passes through the PRODUCTION guard stack: the request goes not to an
// unguarded router but to the one assembled in the same order as in cmd/server
// (see setUpHarness).
func storefrontRequest(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	return keyedStorefrontRequest(t, publishableKey, method, path, body)
}

// storefrontData returns the response envelope's "data" field as an object.
func storefrontData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope),
		"could not decode the response; body: %s", rec.Body.String())
	require.NotNil(t, envelope.Data,
		"the response must carry a singular envelope; body: %s", rec.Body.String())

	return envelope.Data
}
