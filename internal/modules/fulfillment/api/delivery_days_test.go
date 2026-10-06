package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// TestAnOptionsDeliveryDaysAreWrittenAndRead is ADR 0421 on the admin routes:
// the create body carries the days to the service and the record carries them
// back; an update leaves them out, replaces them, or clears them on its own
// flag.
func TestAnOptionsDeliveryDaysAreWrittenAndRead(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{option: models.ShippingOption{
		ID: "sopt_1", Name: "Courier", ProviderID: "manual", ShippingProfileID: "sprof_1",
		PriceType: models.PriceFlat, Amount: 900, CurrencyCode: "TRY",
		DeliveryDays: &models.DeliveryDays{Min: 3, Max: 5},
	}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/admin/v1/shipping-options",
		`{"name":"Courier","provider_id":"manual","shipping_profile_id":"sprof_1",`+
			`"amount":900,"currency_code":"TRY","delivery_days":{"min":3,"max":5}}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, &models.DeliveryDays{Min: 3, Max: 5}, svc.lastOptionInput.DeliveryDays)
	data, ok := bodyMap(t, rec)["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, map[string]any{"min": float64(3), "max": float64(5)}, data["delivery_days"])

	rec = doRequest(t, r, http.MethodPatch, "/admin/v1/shipping-options/sopt_1", `{"name":"Courier"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Nil(t, svc.lastUpdateOption.DeliveryDays, "an update naming no days leaves them")
	assert.False(t, svc.lastUpdateOption.ClearDeliveryDays)

	rec = doRequest(t, r, http.MethodPatch, "/admin/v1/shipping-options/sopt_1", `{"delivery_days":{"min":1,"max":2}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, &models.DeliveryDays{Min: 1, Max: 2}, svc.lastUpdateOption.DeliveryDays)

	rec = doRequest(t, r, http.MethodPatch, "/admin/v1/shipping-options/sopt_1", `{"clear_delivery_days":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, svc.lastUpdateOption.ClearDeliveryDays)
	assert.Nil(t, svc.lastUpdateOption.DeliveryDays)
}

// TestTheListingsPublishAnOptionsDeliveryDays is ADR 0421 on the two
// eligibility listings: an option that says how many business days it takes
// carries them on the storefront's and on the admin's, and one that says none
// carries no key, so the storefront record keeps its five fields.
func TestTheListingsPublishAnOptionsDeliveryDays(t *testing.T) {
	t.Parallel()

	quoted := sampleQuoted()
	bare := quoted[0]
	bare.Option.ID = "sopt_2"
	quoted[0].Option.DeliveryDays = &models.DeliveryDays{Min: 2, Max: 4}
	quoted = append(quoted, bare)
	r := newRouter(&fakeFulfillments{quoted: quoted})

	for _, path := range []string{
		"/store/v1/shipping-options?currency_code=TRY",
		"/admin/v1/shipping-options/eligible?currency_code=TRY",
	} {
		rec := doRequest(t, r, http.MethodGet, path, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		data, ok := bodyMap(t, rec)["data"].([]any)
		require.True(t, ok, rec.Body.String())
		require.Len(t, data, 2)
		withDays, ok := data[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, map[string]any{"min": float64(2), "max": float64(4)}, withDays["delivery_days"], path)
		without, ok := data[1].(map[string]any)
		require.True(t, ok)
		assert.NotContains(t, without, "delivery_days", "%s: an option that says none", path)
	}

	rec := doRequest(t, r, http.MethodGet, "/store/v1/shipping-options?currency_code=TRY", "")
	data, ok := bodyMap(t, rec)["data"].([]any)
	require.True(t, ok, rec.Body.String())
	assert.Len(t, data[0], 6, "the storefront record carries the days beside its five fields")
	assert.Len(t, data[1], 5)
}
