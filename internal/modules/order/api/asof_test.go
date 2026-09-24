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
	for _, list := range []string{"claims", "exchanges", "replacements", "shipments"} {
		assert.JSONEq(t, `[]`, string(body.Data[list]), "%s is an empty array, not null", list)
	}
	assert.Contains(t, string(body.Data["lines"]), `"canceled":1`)
	assert.Contains(t, string(body.Data["returns"]), `"status":"received"`)
}
