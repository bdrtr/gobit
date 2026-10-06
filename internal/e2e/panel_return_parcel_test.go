//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	fulfillmentmanual "github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	fulfillmentsvc "github.com/bdrtr/gobit/internal/modules/fulfillment/service"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// panelReturnParcelOpened reads the parcel the return's open form reports.
var panelReturnParcelOpened = regexp.MustCompile(`Parcel (ful_[0-9A-Z]+) was opened to bring the return back\.`)

// TestAnOperatorBringsAReturnBackFromThePanel is ADR 0413 on the production
// wiring: a shipped order's return is offered a parcel on the region's return
// option for the units it awaits; the form opens one through the fulfillment
// module's surface, named by the return, and the same form sent again opens
// nothing new; the parcel is listed under its return and not among the order's
// own shipments, and is marked shipped with the tracking number the customer
// gave. A move naming it under another order's page is refused and moves
// nothing (gap D266).
func TestAnOperatorBringsAReturnBackFromThePanel(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Panel Return Parcel",
		map[string]int64{taxedCurrency: cancelUnitPrice}, cancelInitialStock)
	cartID, _ := prepareCart(ctx, t, customerID, variantID, cancelQuantity)
	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID: cartID, LocationID: stockLocationID, PaymentProviderID: paymentmanual.ID,
		PaymentData: paymentBehavior(t, paymentmanual.OutcomeAuthorize), Email: email,
		ExpectedTotal: cancelTotal,
	})
	require.NoError(t, err)
	order, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	lineID := order.Items[0].ID

	profileID := newShippingProfile(ctx, t, "E2E Panel Return Profile")
	outgoingID := newShippingOption(ctx, t, profileID, "E2E Panel Return Out", shippingOptionFee, false)
	back, err := shippingSvc.CreateShippingOption(ctx, fulfillmentsvc.CreateOptionInput{
		Name:       fmt.Sprintf("E2E Panel Return Back %d", fixtureCounter.Add(1)),
		ProviderID: fulfillmentmanual.ID, ShippingProfileID: profileID, Amount: shippingOptionFee,
		CurrencyCode: taxedCurrency, RegionID: taxedRegionID, IsReturn: true,
	})
	require.NoError(t, err)

	out, err := adminRequestWithBody(http.MethodPost, "/admin/v1/fulfillments", map[string]any{
		"reference": placed.OrderID, "shipping_option_id": outgoingID,
		"idempotency_key": "panel-return-out-" + placed.OrderID,
		"items":           []map[string]any{{"line_item_id": lineID, "quantity": 2}},
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, out.Code, out.Body.String())
	var opened struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &opened))
	outID := opened.Data.ID
	for _, step := range []string{"ship", "deliver"} {
		rec, err := adminRequestWithBody(http.MethodPost, "/admin/v1/fulfillments/"+outID+"/"+step, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, rec.Code, "%s: %s", step, rec.Body.String())
	}
	requested := storefrontRequest(t, http.MethodPost, "/store/v1/orders/"+placed.OrderID+"/returns",
		`{"lines":[{"order_line_item_id":"`+lineID+`","quantity":2}]}`)
	require.Equal(t, http.StatusCreated, requested.Code, requested.Body.String())
	var asked afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(requested.Body.Bytes(), &asked))
	returnID := asked.Data.ID

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_returns", Kind: "user",
			Scopes: []string{"order:read", "order:write", "fulfillment:read", "fulfillment:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	pagePath := adminui.OrdersPath + "/" + placed.OrderID
	page := send(http.MethodGet, pagePath, nil).Body.String()
	action := `action="` + pagePath + `/returns/` + returnID + `/parcels"`
	require.Contains(t, page, action, "the requested return offers a parcel")
	assert.Contains(t, page, `<option value="`+back.ID+`">`, "on the region's return option")
	assert.NotContains(t, page, `<option value="`+outgoingID+`">`, "and not on an outgoing one")
	key := regexp.MustCompile(`returns/` + returnID + `/parcels">\s*<input type="hidden" name="key" value="(panel-[0-9a-f]+)"`).
		FindStringSubmatch(page)
	require.Len(t, key, 2, "the form carries a key")
	assert.Contains(t, page, `name="units_`+lineID+`" min="0" max="2" value="2"`, "the two units the return awaits")

	form := url.Values{"key": {key[1]}, "option": {back.ID}, "units_" + lineID: {"2"}}
	rec := send(http.MethodPost, pagePath+"/returns/"+returnID+"/parcels", form)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	parcel := panelReturnParcelOpened.FindStringSubmatch(rec.Body.String())
	require.Len(t, parcel, 2, "the parcel is opened: %s", rec.Body.String())
	parcelID := parcel[1]
	opened2, err := shippingSvc.GetFulfillment(ctx, parcelID)
	require.NoError(t, err)
	assert.Equal(t, returnID, opened2.ReturnID, "the parcel names the return it brings back")

	rec = send(http.MethodPost, pagePath+"/returns/"+returnID+"/parcels", form)
	assert.Contains(t, rec.Body.String(), "This form had already opened parcel "+parcelID+"; nothing new was opened.")

	listed := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+placed.OrderID+"/fulfillments", "")
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	assert.NotContains(t, listed.Body.String(), parcelID, "it is not one of the order's shipments")

	page = send(http.MethodGet, pagePath, nil).Body.String()
	assert.Contains(t, page, `action="`+pagePath+`/parcels/`+parcelID+`/ship"`, "listed under its return and moved like any")
	assert.NotContains(t, page, action, "the return awaits nothing more")
	assert.NotContains(t, page, outID, "the order's delivered parcel is listed under no return")

	rec = send(http.MethodPost, pagePath+"/parcels/"+parcelID+"/ship", url.Values{"tracking_number": {"RT-41"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	shipped, err := shippingSvc.GetFulfillment(ctx, parcelID)
	require.NoError(t, err)
	assert.Equal(t, "shipped", string(shipped.Status))
	assert.Equal(t, "RT-41", shipped.TrackingNumber)

	elsewhere := send(http.MethodPost, adminui.OrdersPath+"/order_elsewhere/parcels/"+parcelID+"/deliver", url.Values{})
	assert.Equal(t, http.StatusNotFound, elsewhere.Code, elsewhere.Body.String())
	still, err := shippingSvc.GetFulfillment(ctx, parcelID)
	require.NoError(t, err)
	assert.Equal(t, "shipped", string(still.Status), "a move under another order's page moves nothing")
}
