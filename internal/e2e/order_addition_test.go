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
)

// This file proves ADR 0192 on the production wiring: a customer adds to a
// pending order by opening a cart that names it, and the checkout places an
// ordinary order that says so.
//
// The unit tests hold each hop: the cart workflow asks the order module, the
// cart hands the id to the checkout, the checkout to the order, the order
// checks it under a lock. What only this file can say is that the five hops are
// one path: the id the operator typed is the id the placed order carries, each
// module spells it the way the next one reads it, and a refusal raised in the
// order module reaches the HTTP answer with its own code.

const (
	// additionUnitPrice is the price the catalog holds for the fixture variant.
	additionUnitPrice int64 = 10_000
	// additionTotal is one unit in the taxed region (20%), computed by hand.
	additionTotal int64 = 12_000
	// additionStock is what the fixture puts on the shelf.
	additionStock int64 = 10
)

// additionFixture is one customer, one stocked variant and one pending order of
// theirs.
type additionFixture struct {
	customerID  string
	email       string
	variantID   string
	stockItemID string
	parentID    string
}

// newAdditionFixture places a pending order for a fresh customer.
func newAdditionFixture(t *testing.T) additionFixture {
	t.Helper()

	ctx := t.Context()
	f := additionFixture{}
	f.customerID, f.email = newCustomer(ctx, t)
	f.variantID, f.stockItemID = newStockedVariant(ctx, t, "E2E Addition", map[string]int64{
		taxedCurrency: additionUnitPrice,
	}, additionStock)
	f.parentID = f.checkout(t, f.openCart(t, ""))

	return f
}

// openCart opens an admin cart for the fixture's customer, adding to addsTo
// when it is not empty, and puts one unit in it.
func (f additionFixture) openCart(t *testing.T, addsTo string) string {
	t.Helper()

	opened := openAdditionCart(t, f.customerID, f.email, addsTo)
	require.Equal(t, http.StatusCreated, opened.Code, "body: %s", opened.Body.String())

	cart := storefrontData(t, opened)
	cartID, ok := cart["id"].(string)
	require.True(t, ok, "body: %s", opened.Body.String())

	added := addAdminLine(t, cartID, testChannelID, f.variantID, 1)
	require.Equal(t, http.StatusCreated, added.Code, "body: %s", added.Body.String())

	return cartID
}

// checkout completes the cart on the storefront and returns the order's id.
func (f additionFixture) checkout(t *testing.T, cartID string) string {
	t.Helper()

	done := completeAdditionCart(t, cartID)
	require.Equal(t, http.StatusOK, done.Code, "body: %s", done.Body.String())

	orderID, _ := storefrontData(t, done)["order_id"].(string)
	require.NotEmpty(t, orderID)

	return orderID
}

// openAdditionCart opens an admin cart, naming the order it adds to.
func openAdditionCart(t *testing.T, customerID, email, addsTo string) *httptest.ResponseRecorder {
	t.Helper()

	return adminCartRequest(t, http.MethodPost, "/admin/v1/carts",
		fmt.Sprintf(`{"country_code":%q,"customer_id":%q,"email":%q,"adds_to_order_id":%q}`,
			taxedCountry, customerID, email, addsTo))
}

// completeAdditionCart completes the cart on the storefront at the fixture's
// hand-computed total.
func completeAdditionCart(t *testing.T, cartID string) *httptest.ResponseRecorder {
	t.Helper()

	return storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, additionTotal))
}

// orderList is the admin listing's envelope, read as far as these tests need.
type orderList struct {
	Data  []map[string]any `json:"data"`
	Count float64          `json:"count"`
}

// decodeList decodes an admin listing.
func decodeList(t *testing.T, rec *httptest.ResponseRecorder) orderList {
	t.Helper()

	var page orderList
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page), "body: %s", rec.Body.String())

	return page
}

// TestACustomerAddsToAPendingOrder is the whole act: the addition is an order
// of its own that names its parent on both surfaces, the parent's additions
// are one read, and the parent is left as it was.
func TestACustomerAddsToAPendingOrder(t *testing.T) {
	f := newAdditionFixture(t)

	cartID := f.openCart(t, f.parentID)
	cart := storefrontData(t, adminCartRequest(t, http.MethodGet, "/admin/v1/carts/"+cartID, ""))
	assert.Equal(t, f.parentID, cart["adds_to_order_id"], "the cart keeps the order it adds to")

	additionID := f.checkout(t, cartID)
	require.NotEqual(t, f.parentID, additionID)

	read := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+additionID, "")
	require.Equal(t, http.StatusOK, read.Code, "body: %s", read.Body.String())
	addition := storefrontData(t, read)
	assert.Equal(t, f.parentID, addition["adds_to_order_id"])
	assert.Equal(t, f.customerID, addition["customer_id"])
	assert.InDelta(t, float64(additionTotal), addition["total"], 0,
		"the addition's total is its own, priced and taxed by its own cart")

	shown := storefrontRequest(t, http.MethodGet, "/store/v1/orders/"+additionID, "")
	require.Equal(t, http.StatusOK, shown.Code, "body: %s", shown.Body.String())
	assert.Equal(t, f.parentID, storefrontData(t, shown)["adds_to_order_id"],
		"the shopper sees what their order adds to")

	listed := adminCartRequest(t, http.MethodGet, "/admin/v1/orders?adds_to_order_id="+f.parentID, "")
	require.Equal(t, http.StatusOK, listed.Code, "body: %s", listed.Body.String())
	page := decodeList(t, listed)
	assert.InDelta(t, 1, page.Count, 0)
	require.Len(t, page.Data, 1)
	assert.Equal(t, additionID, page.Data[0]["id"])

	parent := storefrontData(t, adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+f.parentID, ""))
	assert.NotContains(t, parent, "adds_to_order_id")
	assert.InDelta(t, float64(additionTotal), parent["total"], 0, "nothing is summed into the parent")
	assert.Equal(t, "pending", parent["status"])

	assert.Equal(t, additionStock-2, sellableQuantity(t.Context(), t, f.stockItemID),
		"the addition's unit left the shelf as a sale, as the parent's did")
}

// TestAnAdditionIsRefusedWhereTheOrderSays reaches every refusal through the
// HTTP answer, with the order module's own codes, and opens no cart.
func TestAnAdditionIsRefusedWhereTheOrderSays(t *testing.T) {
	f := newAdditionFixture(t)
	otherCustomer, otherEmail := newCustomer(t.Context(), t)
	addition := f.checkout(t, f.openCart(t, f.parentID))

	for _, tc := range []struct {
		name   string
		open   func() *httptest.ResponseRecorder
		status int
		code   string
	}{
		{
			name: "a guest on the storefront",
			open: func() *httptest.ResponseRecorder {
				return storefrontRequest(t, http.MethodPost, "/store/v1/carts",
					fmt.Sprintf(`{"country_code":%q,"adds_to_order_id":%q}`, taxedCountry, f.parentID))
			},
			status: http.StatusConflict, code: "order_addition_needs_customer",
		},
		{
			name:   "another customer",
			open:   func() *httptest.ResponseRecorder { return openAdditionCart(t, otherCustomer, otherEmail, f.parentID) },
			status: http.StatusConflict, code: "order_addition_customer_mismatch",
		},
		{
			name:   "an addition",
			open:   func() *httptest.ResponseRecorder { return openAdditionCart(t, f.customerID, f.email, addition) },
			status: http.StatusConflict, code: "order_addition_parent_is_addition",
		},
		{
			name:   "no such order",
			open:   func() *httptest.ResponseRecorder { return openAdditionCart(t, f.customerID, f.email, "order_NOWHERE") },
			status: http.StatusNotFound, code: "order_not_found",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refused := tc.open()
			assert.Equal(t, tc.status, refused.Code, "body: %s", refused.Body.String())
			assert.Equal(t, tc.code, errorCode(t, refused))
		})
	}
}

// TestAnOrderClosedAfterItsCartOpenedRefusesTheCheckout is the second asking:
// the parent was pending when the cart was opened and is completed before the
// cart is checked out. The order module refuses the write under its lock, which
// comes before any payment, and the saga gives the stock back.
func TestAnOrderClosedAfterItsCartOpenedRefusesTheCheckout(t *testing.T) {
	f := newAdditionFixture(t)
	cartID := f.openCart(t, f.parentID)

	completed := adminCartRequest(t, http.MethodPost, "/admin/v1/orders/"+f.parentID+"/complete", "")
	require.Equal(t, http.StatusOK, completed.Code, "body: %s", completed.Body.String())

	refused := completeAdditionCart(t, cartID)
	assert.Equal(t, http.StatusConflict, refused.Code, "body: %s", refused.Body.String())
	assert.Equal(t, "order_addition_parent_not_pending", errorCode(t, refused))

	listed := adminCartRequest(t, http.MethodGet, "/admin/v1/orders?adds_to_order_id="+f.parentID, "")
	require.Equal(t, http.StatusOK, listed.Code, "body: %s", listed.Body.String())
	assert.InDelta(t, 0, decodeList(t, listed).Count, 0, "no addition was placed")
	assert.Equal(t, additionStock-1, sellableQuantity(t.Context(), t, f.stockItemID),
		"the reservation the saga made for the refused addition was released")
}
