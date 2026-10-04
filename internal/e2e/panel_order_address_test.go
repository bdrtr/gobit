//go:build integration

package e2e

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/adminui"
)

// shipToInputs reads the address correction form as a browser fills it:
// every input's name and the value drawn in it.
func shipToInputs(t *testing.T, page string) url.Values {
	t.Helper()

	_, form, found := strings.Cut(page, `/shipping-address">`)
	require.True(t, found, "the order page offers the correction")
	form, _, _ = strings.Cut(form, "</form>")
	values := url.Values{}
	for _, input := range regexp.MustCompile(`<input[^>]*name="([^"]+)"[^>]*value="([^"]*)"`).FindAllStringSubmatch(form, -1) {
		values.Set(input[1], html.UnescapeString(input[2]))
	}

	return values
}

// timelineCount counts the order's timeline entries of the kind.
func timelineCount(t *testing.T, orderID, kind string) int {
	t.Helper()

	rec := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+orderID+"/timeline", "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	return strings.Count(rec.Body.String(), `"`+kind+`"`)
}

// TestAnOperatorCorrectsAnAddressInThePanel is ADR 0388 on the production
// wiring: the form drawn with the order carries every field and the row read,
// and changing one line keeps the others; the same form again writes nothing;
// a form drawn before is refused with what was typed; once a parcel is on its
// way no form is drawn and the flow refuses one sent anyway.
func TestAnOperatorCorrectsAnAddressInThePanel(t *testing.T) {
	_, orderID := addressedParent(t)
	send := orderWriter(t)
	pagePath := adminui.OrdersPath + "/" + orderID

	drawn := shipToInputs(t, send(http.MethodGet, pagePath, nil).Body.String())
	require.NotEmpty(t, drawn.Get("read_address"))
	for _, key := range []string{"first_name", "last_name", "company", "address_1", "address_2", "city", "province", "postal_code", "phone"} {
		assert.True(t, drawn.Has(key), "the form draws %s", key)
	}
	assert.Equal(t, "9 Far Road", drawn.Get("address_1"))
	stale := url.Values{}
	for key, values := range drawn {
		stale[key] = values
	}

	drawn.Set("address_1", "10 Far Road")
	corrected := send(http.MethodPost, pagePath+"/shipping-address", drawn)
	require.Equal(t, http.StatusOK, corrected.Code, corrected.Body.String())
	assert.Contains(t, corrected.Body.String(), "The order now ships to the address typed")
	read := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+orderID, "")
	require.Equal(t, http.StatusOK, read.Code)
	shipping, ok := storefrontData(t, read)["shipping_address"].(map[string]any)
	require.True(t, ok, read.Body.String())
	assert.Equal(t, "10 Far Road", shipping["address_1"])
	for key, want := range map[string]string{
		"first_name": "Gift", "last_name": "Recipient", "city": "Elsewhere", "postal_code": "11111",
	} {
		assert.Equal(t, want, shipping[key], "%s survives a form that changed one line", key)
	}
	assert.Equal(t, map[string]any{"gate_code": "4411"}, shipping["metadata"], "the metadata is kept")

	again := send(http.MethodPost, pagePath+"/shipping-address", drawn)
	require.Equal(t, http.StatusOK, again.Code, again.Body.String())
	assert.Equal(t, 1, timelineCount(t, orderID, "order.shipping_address_corrected"), "the same form twice corrects once")

	stale.Set("address_2", "Back door")
	refused := send(http.MethodPost, pagePath+"/shipping-address", stale)
	require.Equal(t, http.StatusUnprocessableEntity, refused.Code, refused.Body.String())
	assert.Contains(t, refused.Body.String(), "draw the page again")
	assert.Equal(t, "Back door", shipToInputs(t, refused.Body.String()).Get("address_2"), "what was typed comes back")

	openSpyParcel(t, orderID, spyOption(t))
	hidden := send(http.MethodGet, pagePath, nil).Body.String()
	assert.Contains(t, hidden, `/shipping-address">`, "parcels the page may not read are left to the flow")
	reader := orderWriter(t, "fulfillment:read")
	page := reader(http.MethodGet, pagePath, nil).Body.String()
	assert.NotContains(t, page, `/shipping-address">`, "no form while a parcel the page reads is on its way")
	underway := send(http.MethodPost, pagePath+"/shipping-address", drawn)
	require.Equal(t, http.StatusUnprocessableEntity, underway.Code, underway.Body.String())
	assert.Contains(t, underway.Body.String(), "cancel it or wait for it to come back")
}
