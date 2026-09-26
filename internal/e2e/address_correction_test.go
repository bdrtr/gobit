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

// This file proves ADR 0195 on the production wiring: an operator corrects
// where an order ships while nothing is on its way, and every reader of the
// address — the admin record, the timeline and the next parcel's carrier —
// reads the correction.

// correctedBody is the address the operator was told on the phone.
func correctedBody() string {
	return fmt.Sprintf(`{"first_name":"Gift","last_name":"Recipient","address_1":"10 Near Road",`+
		`"city":"Elsewhere","postal_code":"11112","country_code":%q}`, taxedCountry)
}

// correctAddress sends the correction and returns the answer.
func correctAddress(t *testing.T, orderID, body string) (status int, order map[string]any, code string) {
	t.Helper()

	rec := adminCartRequest(t, http.MethodPut, "/admin/v1/orders/"+orderID+"/shipping-address", body)
	if rec.Code != http.StatusOK {
		return rec.Code, nil, errorCode(t, rec)
	}

	return rec.Code, storefrontData(t, rec), ""
}

// TestAnOperatorCorrectsWhereAnOrderShips is the whole act: the correction is
// the admin record's address, the customer's timeline dates it, and the parcel
// opened afterwards carries it to the carrier.
func TestAnOperatorCorrectsWhereAnOrderShips(t *testing.T) {
	orderID := addressedOrder(t)

	status, order, code := correctAddress(t, orderID, correctedBody())
	require.Equal(t, http.StatusOK, status, "code: %s", code)
	shipping, ok := order["shipping_address"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "10 Near Road", shipping["address_1"])
	billing, ok := order["billing_address"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Engines Ltd", billing["company"], "the billing address is untouched")

	timeline := storefrontRequest(t, http.MethodGet, "/store/v1/orders/"+orderID+"/timeline", "")
	require.Equal(t, http.StatusOK, timeline.Code, "body: %s", timeline.Body.String())
	assert.Contains(t, timeline.Body.String(), `"order.shipping_address_corrected"`,
		"the customer who rang sees that it was done")
	assert.NotContains(t, timeline.Body.String(), "Near Road", "the timeline carries no address")

	key, _ := openSpyParcel(t, orderID, spyOption(t))
	handed, ok := carrierSpy.shipmentFor(key)
	require.True(t, ok)
	require.NotNil(t, handed.Destination)
	assert.Equal(t, "10 Near Road", handed.Destination.Address1,
		"the parcel opened after the correction goes where the correction says")
}

// TestAParcelOnItsWayStopsACorrectionUntilItIsCanceled refuses while the
// carrier holds the old address, and allows the correction once the parcel is
// withdrawn.
func TestAParcelOnItsWayStopsACorrectionUntilItIsCanceled(t *testing.T) {
	orderID := addressedOrder(t)
	_, fulfillmentID := openSpyParcel(t, orderID, spyOption(t))

	status, _, code := correctAddress(t, orderID, correctedBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "fulfilling_parcel_underway", code)

	canceled, err := adminRequestWithBody(http.MethodPost, "/admin/v1/fulfillments/"+fulfillmentID+"/cancel", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, canceled.Code, "body: %s", canceled.Body.String())

	status, order, code := correctAddress(t, orderID, correctedBody())
	require.Equal(t, http.StatusOK, status, "code: %s", code)
	shipping, ok := order["shipping_address"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "10 Near Road", shipping["address_1"])
}

// TestACorrectionIntoAnotherCountryIsRefused keeps the country the tax and the
// shipping price were computed on.
func TestACorrectionIntoAnotherCountryIsRefused(t *testing.T) {
	orderID := addressedOrder(t)

	status, _, code := correctAddress(t, orderID,
		`{"address_1":"10 Near Road","country_code":"DE"}`)

	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "order_address_country_changed", code)

	read := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+orderID, "")
	var answer struct {
		Data struct {
			ShippingAddress map[string]any `json:"shipping_address"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(read.Body.Bytes(), &answer))
	assert.Equal(t, "9 Far Road", answer.Data.ShippingAddress["address_1"], "nothing was written")
}
