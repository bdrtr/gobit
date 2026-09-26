//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file proves ADR 0197 on the production wiring: an addition joins a
// pending parcel of the order it adds to, the parcel answers for both, and the
// widened link lets one parcel be bound to two orders.

// joinParcel binds the order to the parcel through the admin surface.
func joinParcel(t *testing.T, orderID, fulfillmentID string) (status int, body, code string) {
	t.Helper()

	rec := adminCartRequest(t, http.MethodPut,
		"/admin/v1/orders/"+orderID+"/fulfillments/"+fulfillmentID, "")
	if rec.Code != http.StatusOK {
		return rec.Code, rec.Body.String(), errorCode(t, rec)
	}

	return rec.Code, rec.Body.String(), ""
}

// TestAnAdditionTravelsInItsParentsParcel is the whole act: the addition,
// which recorded no address of its own, joins the parent's waiting parcel, and
// the parcel is then among the addition's shipments and on its timeline.
func TestAnAdditionTravelsInItsParentsParcel(t *testing.T) {
	f, parentID := addressedParent(t)
	_, parcelID := openSpyParcel(t, parentID, spyOption(t))
	additionID := f.checkout(t, f.openCart(t, parentID))

	status, body, code := joinParcel(t, additionID, parcelID)
	require.Equal(t, http.StatusOK, status, "code %s: %s", code, body)
	assert.Contains(t, body, parcelID, "the answer is the addition's shipments, the parcel among them")

	again, _, code := joinParcel(t, additionID, parcelID)
	assert.Equal(t, http.StatusOK, again, "joining twice binds nothing new; code %s", code)

	listed := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+parentID+"/fulfillments", "")
	require.Equal(t, http.StatusOK, listed.Code)
	assert.Contains(t, listed.Body.String(), parcelID, "the parcel is still the parent's")

	timeline := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+additionID+"/timeline", "")
	require.Equal(t, http.StatusOK, timeline.Code, "body: %s", timeline.Body.String())
	assert.Contains(t, timeline.Body.String(), `"shipment.opened"`,
		"the addition's timeline shows the parcel it travels in")
}

// TestAnAdditionGoingElsewhereDoesNotJoin refuses an addition whose own
// shipping address is not its parent's, and a parcel no longer waiting.
func TestAnAdditionGoingElsewhereDoesNotJoin(t *testing.T) {
	f, parentID := addressedParent(t)
	_, parcelID := openSpyParcel(t, parentID, spyOption(t))

	cartID := f.openCart(t, parentID)
	elsewhere := fmt.Sprintf(`{"first_name":"Someone","address_1":"1 Other St","city":"Elsewhere",`+
		`"postal_code":"22222","country_code":%q}`, taxedCountry)
	rec := storefrontRequest(t, http.MethodPut, "/store/v1/carts/"+cartID+"/shipping-address", elsewhere)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	additionID := f.checkout(t, cartID)

	status, _, code := joinParcel(t, additionID, parcelID)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "order_ships_elsewhere", code)

	canceled, err := adminRequestWithBody(http.MethodPost, "/admin/v1/fulfillments/"+parcelID+"/cancel", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, canceled.Code, "body: %s", canceled.Body.String())

	plain := f.checkout(t, f.openCart(t, parentID))
	status, _, code = joinParcel(t, plain, parcelID)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "fulfilling_parcel_not_waiting", code)
}
