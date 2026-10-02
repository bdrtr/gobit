package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// TestTheStorefrontAddressBookCarriesTheProvince is ADR 0369 at the HTTP
// edge: the province a shopper writes reaches the service on a new address
// and on a patch, and an address answers with the one it holds.
func TestTheStorefrontAddressBookCarriesTheProvince(t *testing.T) {
	var created service.AddressInput
	var patched service.UpdateAddressInput
	svc := &stubCustomer{
		createAddressFn: func(_ context.Context, _ string, in service.AddressInput) (models.CustomerAddress, error) {
			created = in
			return models.CustomerAddress{ID: "addr_1", CustomerID: "cust_1", Province: in.Province}, nil
		},
		updateAddressFn: func(_ context.Context, _, _ string, in service.UpdateAddressInput) (models.CustomerAddress, error) {
			patched = in
			return models.CustomerAddress{ID: "addr_1", CustomerID: "cust_1", Province: *in.Province}, nil
		},
	}
	r := routerWithIdentity(svc, pathProvingIdentity{})

	rec := send(t, r, http.MethodPost, "/store/v1/customers/cust_1/addresses",
		`{"address_1":"Cad. 1","city":"Konak","province":"Izmir","country_code":"tr"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "Izmir", created.Province)
	assert.Equal(t, "Izmir", answeredAddress(t, rec)["province"])

	rec = send(t, r, http.MethodPut, "/store/v1/customers/cust_1/addresses/addr_1", `{"province":"Manisa"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, patched.Province)
	assert.Equal(t, "Manisa", *patched.Province)
	assert.Nil(t, patched.City, "a patch names only what it moves")
	assert.Equal(t, "Manisa", answeredAddress(t, rec)["province"])
}

// answeredAddress is the address an answer carries under data.
func answeredAddress(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())

	return body.Data
}
