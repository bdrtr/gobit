//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file proves ADR 0193 on the production wiring: the addresses a shopper
// gives the cart reach the two readers B11 wrote them for — the operator's
// order record and the invoice's buyer (D140).
//
// Each hop has a unit test: the order's admin record carries its addresses,
// the order's invoice surface carries the billing address, the invoicing flow
// fills an empty buyer from it. What only this file can say is that the cart's
// address survives the checkout into the order and comes back out through both,
// spelled the way each next module reads it.

// addressedOrder places an order whose cart the shopper gave a shipping and a
// billing address on the storefront, and returns its id.
func addressedOrder(t *testing.T) string {
	t.Helper()

	_, orderID := addressedParent(t)

	return orderID
}

// addressedParent is addressedOrder with the fixture it was placed from, so a
// test can place additions to it for the same customer.
func addressedParent(t *testing.T) (fixture additionFixture, orderID string) {
	t.Helper()

	ctx := t.Context()
	customerID, email := newCustomer(ctx, t)
	variantID, stockItemID := newStockedVariant(ctx, t, "E2E Addressed", map[string]int64{
		taxedCurrency: additionUnitPrice,
	}, additionStock)
	fixture = additionFixture{customerID: customerID, email: email, variantID: variantID, stockItemID: stockItemID}

	cartID := fixture.openCart(t, "")

	shipping := fmt.Sprintf(`{"first_name":"Gift","last_name":"Recipient","address_1":"9 Far Road",`+
		`"city":"Elsewhere","postal_code":"11111","country_code":%q,"metadata":{"gate_code":"4411"}}`,
		taxedCountry)
	billing := fmt.Sprintf(`{"first_name":"Ada","last_name":"Lovelace","company":"Engines Ltd",`+
		`"address_1":"12 Main St","address_2":"Floor 3","city":"Springfield","province":"North",`+
		`"postal_code":"62701","country_code":%q}`, taxedCountry)
	for path, body := range map[string]string{"shipping-address": shipping, "billing-address": billing} {
		rec := storefrontRequest(t, http.MethodPut, "/store/v1/carts/"+cartID+"/"+path, body)
		require.Equal(t, http.StatusOK, rec.Code, "%s: %s", path, rec.Body.String())
	}

	orderID = fixture.checkout(t, cartID)
	fixture.parentID = orderID

	return fixture, orderID
}

// TestTheOperatorReadsWhereAnOrderWent reads the two addresses back on the
// admin record, and finds neither on the storefront's.
func TestTheOperatorReadsWhereAnOrderWent(t *testing.T) {
	orderID := addressedOrder(t)

	read := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+orderID, "")
	require.Equal(t, http.StatusOK, read.Code, "body: %s", read.Body.String())
	order := storefrontData(t, read)

	shipping, ok := order["shipping_address"].(map[string]any)
	require.True(t, ok, "the operator reads where the order went; body: %s", read.Body.String())
	assert.Equal(t, "Gift", shipping["first_name"])
	assert.Equal(t, "9 Far Road", shipping["address_1"])
	assert.Equal(t, "11111", shipping["postal_code"])
	assert.Equal(t, taxedCountry, shipping["country_code"])
	assert.Equal(t, map[string]any{"gate_code": "4411"}, shipping["metadata"],
		"the address's own free data travels with it")

	billing, ok := order["billing_address"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Engines Ltd", billing["company"])

	shown := storefrontRequest(t, http.MethodGet, "/store/v1/orders/"+orderID, "")
	require.Equal(t, http.StatusOK, shown.Code, "body: %s", shown.Body.String())
	assert.NotContains(t, storefrontData(t, shown), "shipping_address",
		"the storefront read, open to whoever holds the id, carries no address")
}

// TestAnInvoiceBillsWhomTheOrderWasBilledTo issues a document from a body that
// names no buyer, and reads the order's billing address printed on it.
func TestAnInvoiceBillsWhomTheOrderWasBilledTo(t *testing.T) {
	orderID := addressedOrder(t)
	writeStoreProfile(t)

	recorder, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/invoice",
		map[string]any{"series_prefix": invoiceSeriesPrefix, "buyer": map[string]any{"tax_number": "1111111111"}})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, recorder.Code, "body: %s", recorder.Body.String())

	var issued invoiceIssueResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &issued))

	document, err := adminRequestWithBody(http.MethodGet, "/admin/v1/invoices/"+issued.Data.InvoiceID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, document.Code, "body: %s", document.Body.String())

	var printed struct {
		Data struct {
			Buyer struct {
				Name        string `json:"name"`
				TaxNumber   string `json:"tax_number"`
				Address     string `json:"address"`
				CountryCode string `json:"country_code"`
			} `json:"buyer"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(document.Body.Bytes(), &printed), "body: %s", document.Body.String())

	buyer := printed.Data.Buyer
	assert.Equal(t, "Engines Ltd", buyer.Name, "a document billed to a company is issued to it")
	assert.Equal(t, "12 Main St\nFloor 3\n62701 Springfield North", buyer.Address)
	assert.Equal(t, taxedCountry, buyer.CountryCode)
	assert.Equal(t, "1111111111", buyer.TaxNumber, "what the order does not know is the caller's")
}
