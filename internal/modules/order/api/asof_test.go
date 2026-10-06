package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// The order read at a past moment (ADR 0171).

// TestAMomentIsRequiredAndNotAssumed refuses a missing or unreadable moment
// before the service is asked: taking either as "now" would answer a question
// about the past with the present.
func TestAMomentIsRequiredAndNotAssumed(t *testing.T) {
	for name, query := range map[string]string{
		"no moment":          "",
		"not RFC 3339":       "?at=yesterday",
		"a date and no time": "?at=2026-09-20",
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeOrders{}

			rec := doRequest(t, newRouter(svc), http.MethodGet, "/admin/v1/orders/order_1/as-of"+query, "")

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			assert.Empty(t, svc.calls, "a moment that could not be read must not reach the service")
		})
	}
}

// TestTheReadingIsPublishedAsTheServiceDerivedIt carries the moment to the
// service in UTC and the answer back with every list an array and an unknown
// status a null.
func TestTheReadingIsPublishedAsTheServiceDerivedIt(t *testing.T) {
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	svc := &fakeOrders{asOf: models.OrderAsOf{
		OrderID: "order_1", At: at,
		Money:   models.MoneyAsOf{Currency: "TRY", Total: 6100, Captured: 6100, Refunded: 500, Outstanding: 500},
		Lines:   []models.LineAsOf{{LineItemID: "oli_1", Quantity: 3, Canceled: 1}},
		Returns: []models.RecordAsOf{{ID: "oret_1", Status: "received", Since: at}},
		Contact: models.ContactErasedSince,
	}}

	rec := doRequest(t, newRouter(svc), http.MethodGet,
		"/admin/v1/orders/order_1/as-of?at=2026-09-20T15:00:00%2B03:00", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "order_1", svc.gotOrderID)
	assert.True(t, svc.gotAsOfAt.Equal(at), "the moment reaches the service as the same instant")
	assert.Equal(t, time.UTC, svc.gotAsOfAt.Location())

	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.JSONEq(t, `null`, string(body.Data["status"]), "a status the records cannot place is null")
	assert.JSONEq(t, `"erased_since"`, string(body.Data["contact"]))
	assert.JSONEq(t, `{"currency_code":"TRY","total":6100,"credited":0,"captured":6100,"refunded":500,"outstanding":500}`,
		string(body.Data["money"]))
	for _, list := range []string{"claims", "exchanges", "replacements", "shipments", "deliveries"} {
		assert.JSONEq(t, `[]`, string(body.Data[list]), "%s is an empty array, not null", list)
	}
	assert.JSONEq(t, `null`, string(body.Data["shipping_address"]), "an order with no address names none")
	assert.Contains(t, string(body.Data["lines"]), `"canceled":1`)
	assert.Contains(t, string(body.Data["returns"]), `"status":"received"`)
}

// TestWhereAnOrderWasGoingIsPublishedAsDerived carries the address row and the
// deliveries through (ADR 0411): a row whose content is gone keeps its name and
// moment with a null address, and a delivery as sold carries no change.
func TestWhereAnOrderWasGoingIsPublishedAsDerived(t *testing.T) {
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	since := at.Add(-time.Hour)
	svc := &fakeOrders{asOf: models.OrderAsOf{
		OrderID: "order_1", At: at, Contact: models.ContactErasedSince,
		ShippingAddress: &models.AddressAsOf{ID: "oaddr_2", Since: since},
		Deliveries: []models.DeliveryAsOf{
			{ShippingMethodID: "oship_1", ShippingOptionID: "sopt_std", Name: "Standard", Amount: 1500, Since: since},
			{
				ShippingMethodID: "oship_2", ShippingOptionID: "sopt_pick", Name: "Pickup", Amount: 1000,
				ChangeID: "odchg_1", Difference: -500, CreditLineID: "ocl_1", Since: at,
			},
		},
	}}

	rec := doRequest(t, newRouter(svc), http.MethodGet,
		"/admin/v1/orders/order_1/as-of?at=2026-09-20T12:00:00Z", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.JSONEq(t, `{"id":"oaddr_2","since":"2026-09-20T11:00:00Z","address":null}`,
		string(body.Data["shipping_address"]), "the row is named and its emptied content is not shown")
	assert.JSONEq(t, `[
		{"shipping_method_id":"oship_1","shipping_option_id":"sopt_std","name":"Standard","amount":1500,
		 "difference":0,"since":"2026-09-20T11:00:00Z"},
		{"shipping_method_id":"oship_2","shipping_option_id":"sopt_pick","name":"Pickup","amount":1000,
		 "change_id":"odchg_1","difference":-500,"credit_line_id":"ocl_1","since":"2026-09-20T12:00:00Z"}
	]`, string(body.Data["deliveries"]))
}
