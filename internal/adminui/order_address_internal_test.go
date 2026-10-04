package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// fakeAddressCorrector acts on after-sales records as the shared fake does
// and records each correction: the order, the address as sent and the row
// read.
type fakeAddressCorrector struct {
	fakeAfterSales
	corrected []string
	err       error
}

func (f *fakeAddressCorrector) CorrectShippingAddress(
	_ context.Context, orderID string, address json.RawMessage, readAddressID string,
) error {
	f.corrected = append(f.corrected, orderID+"|"+readAddressID+"|"+string(address))
	return f.err
}

// shippedOrder is the order's own record shipping to Ada, its row named.
var shippedOrder = query.Record{
	fieldShippingAddressID: "oadr_1",
	fieldShippingAddress: map[string]any{
		"first_name": "Ada", "last_name": "Lovelace", "company": "Engines Ltd",
		"address_1": "12 Wrong St", "address_2": "Flat 3", "city": "Springfield",
		"province": "North", "postal_code": "62701", "country_code": "TR", "phone": "+90 555",
	},
}

// shipToCorrection is the correction form on the page, empty when there is none.
func shipToCorrection(body string) string {
	_, form, found := strings.Cut(body, `/shipping-address">`)
	if !found {
		return ""
	}
	form, _, _ = strings.Cut(form, "</form>")

	return form
}

// typedAddress is every field of the form, as a browser sends it.
func typedAddress() url.Values {
	return url.Values{
		formReadAddress: {"oadr_1"}, "first_name": {"Ada"}, "last_name": {"Lovelace"},
		"company": {"Engines Ltd"}, "address_1": {" 12 Right St "}, "address_2": {""},
		"city": {"Springfield"}, "province": {"North"}, "postal_code": {"62701"}, "phone": {"+90 555"},
	}
}

// TestAnOrdersAddressIsCorrectedOnItsPage is ADR 0388: a writer is offered
// the address prefilled, the country fixed, carrying the row it was drawn
// from; the correction is sent whole, a field blanked arriving empty, with
// that row; a form missing a field is refused unsent, and a refusal, the
// address corrected since, comes back with what was typed.
func TestAnOrdersAddressIsCorrectedOnItsPage(t *testing.T) {
	t.Parallel()

	corrector := &fakeAddressCorrector{}
	panel := newCatalogPanel(t, orderCatalogWith("pending", nil, shippedOrder))
	panel.afterSales = corrector
	panel.scopes = builtInScopes()
	page := OrdersPath + "/order_1"
	writer := []string{scopeOrderRead, scopeOrderWrite}

	rec := campaignsRequest(panel, http.MethodGet, page, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	form := shipToCorrection(rec.Body.String())
	require.NotEmpty(t, form, "a writer is offered the correction")
	for _, want := range []string{
		`name="read_address" value="oadr_1"`, `name="address_1" value="12 Wrong St"`,
		`name="address_2" value="Flat 3"`, `name="phone" value="&#43;90 555"`, "in TR, which a correction keeps",
	} {
		assert.Contains(t, form, want)
	}
	assert.NotContains(t, form, `name="country_code"`, "the country is not the form's")
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeOrderRead)
	assert.Empty(t, shipToCorrection(rec.Body.String()), "a reader corrects nothing")

	rec = campaignsRequest(panel, http.MethodPost, page+"/shipping-address", typedAddress(), writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, corrector.corrected, 1)
	orderID, rest, _ := strings.Cut(corrector.corrected[0], "|")
	read, sent, _ := strings.Cut(rest, "|")
	assert.Equal(t, "order_1", orderID)
	assert.Equal(t, "oadr_1", read)
	assert.JSONEq(t, `{"first_name":"Ada","last_name":"Lovelace","company":"Engines Ltd",
		"address_1":"12 Right St","address_2":"","city":"Springfield","province":"North",
		"postal_code":"62701","phone":"+90 555"}`, sent, "the whole address, the blanked line empty")
	assert.Contains(t, rec.Body.String(), "The order now ships to the address typed")

	current, _ := shippedOrder[fieldShippingAddress].(map[string]any)
	for _, key := range orderAddressKeys {
		missing := typedAddress()
		missing.Del(key)
		rec = campaignsRequest(panel, http.MethodPost, page+"/shipping-address", missing, writer...)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, key)
		assert.Contains(t, rec.Body.String(), "The form sent no "+key, key)
		kept := strings.ReplaceAll(stringValue(current[key]), "+", "&#43;")
		assert.Contains(t, shipToCorrection(rec.Body.String()), `name="`+key+`" value="`+kept+`"`,
			"the field not sent is drawn as the order holds it, not cleared: %s", key)
	}
	unread := typedAddress()
	unread.Set(formReadAddress, " ")
	rec = campaignsRequest(panel, http.MethodPost, page+"/shipping-address", unread, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "The address the page was drawn with could not be read")
	assert.Len(t, corrector.corrected, 1, "what the panel cannot read is not sent")

	corrector.err = errors.Conflict("order_address_revised",
		"order order_1's shipping address was corrected since it was read; draw the page again")
	stale := typedAddress()
	stale.Set(formReadAddress, "oadr_0")
	rec = campaignsRequest(panel, http.MethodPost, page+"/shipping-address", stale, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "draw the page again")
	form = shipToCorrection(rec.Body.String())
	assert.Contains(t, form, `name="address_1" value=" 12 Right St "`, "what was typed comes back")
	assert.Contains(t, form, `name="address_2" value=""`)
	assert.Contains(t, form, `name="read_address" value="oadr_1"`, "with the row the order holds, not the one sent")
	assert.Contains(t, rec.Body.String(), "<details open>", "drawn open")

	corrector.err = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/shipping-address", typedAddress(), writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	bare := cancelPanel(t, &fakeAfterSales{})
	rec = campaignsRequest(bare, http.MethodPost, page+"/shipping-address", typedAddress(), writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestAnAddressIsCorrectedOnlyWhereItCanBe: the correction is offered on a
// pending order with a shipping address and no parcel the page reads on its
// way; parcels the page may not read are left to the flow.
func TestAnAddressIsCorrectedOnlyWhereItCanBe(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		status  string
		parcels []string
		record  query.Record
		scopes  []string
		offered bool
	}{
		"pending": {"pending", nil, shippedOrder, []string{scopeOrderRead, scopeOrderWrite}, true},
		"pending, parcels hidden": {"pending", []string{"pending"}, shippedOrder,
			[]string{scopeOrderRead, scopeOrderWrite}, true},
		"pending, a parcel returned": {"pending", []string{"returned"}, shippedOrder,
			[]string{scopeOrderRead, scopeOrderWrite, scopeFulfillmentRead}, true},
		"pending, a parcel pending": {"pending", []string{"pending"}, shippedOrder,
			[]string{scopeOrderRead, scopeOrderWrite, scopeFulfillmentRead}, false},
		"pending, a parcel shipped": {"pending", []string{"shipped"}, shippedOrder,
			[]string{scopeOrderRead, scopeOrderWrite, scopeFulfillmentRead}, false},
		"pending, a parcel delivered": {"pending", []string{"delivered"}, shippedOrder,
			[]string{scopeOrderRead, scopeOrderWrite, scopeFulfillmentRead}, false},
		"completed":  {"completed", nil, shippedOrder, []string{scopeOrderRead, scopeOrderWrite}, false},
		"no address": {"pending", nil, nil, []string{scopeOrderRead, scopeOrderWrite}, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			panel := newCatalogPanel(t, orderCatalogWith(tc.status, tc.parcels, tc.record))
			panel.afterSales = &fakeAddressCorrector{}
			panel.scopes = builtInScopes()
			rec := campaignsRequest(panel, http.MethodGet, OrdersPath+"/order_1", nil, tc.scopes...)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, tc.offered, shipToCorrection(rec.Body.String()) != "")
		})
	}
}
