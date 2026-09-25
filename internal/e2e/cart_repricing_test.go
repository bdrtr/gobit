//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cartResource is the pattern every write on one storefront cart is bound under.
const cartResource = "/store/v1/carts/{id}"

// cartWritesThatEndTheCart are the writes after which there is no cart left to
// be stale: the completion closes it and computes its own total, and the delete
// removes it. Every OTHER write on a storefront cart is in the population below.
var cartWritesThatEndTheCart = map[string]string{
	http.MethodPost + " " + cartResource + "/complete": "closes the cart and prices it itself",
	http.MethodDelete + " " + cartResource:             "deletes the cart",
}

// repricingScenario is one storefront write, arranged so that it CHANGES the
// cart.
//
// arrange prepares what the write needs and returns the concrete path and body.
// A write that changed nothing would prove nothing: a cart whose shape did not
// move is never stale, repriced or not.
type repricingScenario struct {
	arrange func(t *testing.T, cartID string) (path, body string)
}

// TestNoStorefrontWriteLeavesTheCartStale is the gate ADR 0173 rests on.
//
// A storefront cannot compute a total; it reads the one on the cart and sends it
// back as expected_total, and the completion refuses a cart whose total moved
// since. So after EVERY write a shopper can make, the total on the cart has to be
// the one the completion will compute. Seven writes did not do that (D129): the
// e-mail, the two addresses, removing a line, both shipping-method writes and a
// merge left the totals stale, and nothing on the storefront could refresh them —
// a shopper who chose a delivery was shown a total without it and refused at the
// till.
//
// # The population is the ROUTER's
//
// The writes are read off the router, not listed here. A new write under a cart
// fails this test until it has a scenario, and a scenario for a route that is
// gone fails it too; the only routes left out are the two that end the cart.
func TestNoStorefrontWriteLeavesTheCartStale(t *testing.T) {
	ctx := t.Context()

	variantID := newVariant(ctx, t, "Repriced product", map[string]int64{taxedCurrency: 10_000})
	optionID := newShippingOption(ctx, t, newShippingProfile(ctx, t, "Repricing profile"),
		"Repricing option", 4_900, false)
	code := fmt.Sprintf("REPRICE%d", fixtureCounter.Add(1))
	newCouponPromotion(ctx, t, code, 1_000, []string{variantID})

	addLine := func(t *testing.T, cartID string, quantity int) string {
		t.Helper()
		rec := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items",
			fmt.Sprintf(`{"variant_id":%q,"quantity":%d}`, variantID, quantity))
		require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

		return createdID(t, rec)
	}
	address := fmt.Sprintf(`{"first_name":"Re","last_name":"Priced","address_1":"Street 1",`+
		`"city":"City","postal_code":"00000","country_code":%q}`, taxedCountry)
	setAddress := func(t *testing.T, cartID string) {
		t.Helper()
		rec := storefrontRequest(t, http.MethodPut, "/store/v1/carts/"+cartID+"/shipping-address", address)
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	}
	addShipping := func(t *testing.T, cartID string) string {
		t.Helper()
		rec := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/shipping-methods",
			fmt.Sprintf(`{"shipping_option_id":%q}`, optionID))
		require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

		return createdID(t, rec)
	}

	scenarios := map[string]repricingScenario{
		http.MethodPost + " " + cartResource: {func(_ *testing.T, cartID string) (string, string) {
			return "/store/v1/carts/" + cartID, `{"email":"repriced@example.test"}`
		}},
		http.MethodPost + " " + cartResource + "/merge": {func(t *testing.T, cartID string) (string, string) {
			source := openCartWithKey(t, publishableKey)
			addLine(t, source, 2)

			return "/store/v1/carts/" + cartID + "/merge", fmt.Sprintf(`{"source_cart_id":%q}`, source)
		}},
		http.MethodPost + " " + cartResource + "/promotions": {func(_ *testing.T, cartID string) (string, string) {
			return "/store/v1/carts/" + cartID + "/promotions", fmt.Sprintf(`{"code":%q}`, code)
		}},
		http.MethodDelete + " " + cartResource + "/promotions/{code}": {func(t *testing.T, cartID string) (string, string) {
			rec := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/promotions",
				fmt.Sprintf(`{"code":%q}`, code))
			require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

			return "/store/v1/carts/" + cartID + "/promotions/" + code, ""
		}},
		http.MethodPost + " " + cartResource + "/line-items": {func(_ *testing.T, cartID string) (string, string) {
			return "/store/v1/carts/" + cartID + "/line-items",
				fmt.Sprintf(`{"variant_id":%q,"quantity":1}`, variantID)
		}},
		http.MethodPatch + " " + cartResource + "/line-items/{line_item_id}": {func(t *testing.T, cartID string) (string, string) {
			return "/store/v1/carts/" + cartID + "/line-items/" + addLine(t, cartID, 1), `{"quantity":3}`
		}},
		http.MethodDelete + " " + cartResource + "/line-items/{line_item_id}": {func(t *testing.T, cartID string) (string, string) {
			return "/store/v1/carts/" + cartID + "/line-items/" + addLine(t, cartID, 1), ""
		}},
		http.MethodPut + " " + cartResource + "/shipping-address": {func(_ *testing.T, cartID string) (string, string) {
			return "/store/v1/carts/" + cartID + "/shipping-address", address
		}},
		http.MethodPut + " " + cartResource + "/billing-address": {func(_ *testing.T, cartID string) (string, string) {
			return "/store/v1/carts/" + cartID + "/billing-address", address
		}},
		http.MethodPost + " " + cartResource + "/shipping-methods": {func(t *testing.T, cartID string) (string, string) {
			setAddress(t, cartID)

			return "/store/v1/carts/" + cartID + "/shipping-methods",
				fmt.Sprintf(`{"shipping_option_id":%q}`, optionID)
		}},
		http.MethodDelete + " " + cartResource + "/shipping-methods/{shipping_method_id}": {func(t *testing.T, cartID string) (string, string) {
			setAddress(t, cartID)

			return "/store/v1/carts/" + cartID + "/shipping-methods/" + addShipping(t, cartID), ""
		}},
	}

	writes := storefrontCartWrites(t)
	require.GreaterOrEqualf(t, len(writes), len(scenarios),
		"only %d storefront cart writes were read off the router; the walk has gone blind, "+
			"and a blind population passes every assertion below", len(writes))

	for _, write := range writes {
		scenario, known := scenarios[write]
		if !assert.Truef(t, known,
			"%s writes a storefront cart and has no scenario here. Give it one: a write that "+
				"leaves the totals stale shows the shopper a figure the completion refuses", write) {
			continue
		}

		t.Run(write, func(t *testing.T) {
			cartID := openCartWithKey(t, publishableKey)
			addLine(t, cartID, 1)

			path, body := scenario.arrange(t, cartID)
			method, _, _ := strings.Cut(write, " ")
			rec := storefrontRequest(t, method, path, body)
			require.Lessf(t, rec.Code, http.StatusMultipleChoices,
				"the write itself failed, so the test would prove nothing; body: %s", rec.Body.String())

			fetched := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
			require.Equal(t, http.StatusOK, fetched.Code, "body: %s", fetched.Body.String())
			cart := storefrontData(t, fetched)

			assert.Equalf(t, false, cart["totals_stale"],
				"%s left the cart's totals stale (total %v). The storefront shows that figure, "+
					"sends it as expected_total, and the completion refuses it (D129)",
				write, cart["total"])
		})
	}

	for key := range scenarios {
		assert.Containsf(t, writes, key,
			"the scenario for %s names a route the router no longer binds; a stale entry "+
				"here reads as coverage", key)
	}
}

// createdID is the id of the record a storefront write answered with.
func createdID(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	id, ok := storefrontData(t, rec)["id"].(string)
	require.True(t, ok, "the answer carries no id; body: %s", rec.Body.String())

	return id
}

// storefrontCartWrites returns every write bound on one storefront cart, as
// "METHOD pattern", except the two that end the cart.
func storefrontCartWrites(t *testing.T) []string {
	t.Helper()

	var writes []string
	ending := map[string]bool{}
	err := chi.Walk(testRouter, func(
		method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
			return nil
		}
		if pattern != cartResource && !strings.HasPrefix(pattern, cartResource+"/") {
			return nil
		}
		key := method + " " + pattern
		if _, ends := cartWritesThatEndTheCart[key]; ends {
			ending[key] = true
			return nil
		}
		writes = append(writes, key)

		return nil
	})
	require.NoError(t, err, "the router could not be walked")
	for key, why := range cartWritesThatEndTheCart {
		assert.Truef(t, ending[key],
			"%s is left out because it %s, and the router no longer binds it; an exclusion "+
				"that names nothing hides whatever took its place", key, why)
	}
	sort.Strings(writes)

	return writes
}
