package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// TestAShopperCannotChangeTheirAddressFromTheStorefront is ADR 0376 (D224):
// the e-mail address is the one an account signs in under and is mailed at,
// and nothing on the storefront proves a shopper owns a new one, so a body
// carrying it is refused and reaches no service.
func TestAShopperCannotChangeTheirAddressFromTheStorefront(t *testing.T) {
	t.Parallel()

	called := false
	svc := &stubCustomer{
		updateCustomerFn: func(_ context.Context, id string, _ service.UpdateCustomerInput) (models.Customer, error) {
			called = true
			return models.Customer{ID: id}, nil
		},
	}
	r := routerWithIdentity(svc, pathProvingIdentity{})

	rec := send(t, r, http.MethodPut, "/store/v1/customers/cust_1",
		`{"email":"somebody-else@example.test"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.False(t, called, "the address reached no service")

	rec = send(t, r, http.MethodPut, "/store/v1/customers/cust_1",
		`{"first_name":"Ece","email":"somebody-else@example.test"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"a body that also carries a name is refused whole, not applied in part: %s", rec.Body.String())
	assert.False(t, called)
}

// TestAShopperStillChangesTheRestOfTheirProfile: every other field reaches
// the service, and the address is never part of what it is asked to write.
func TestAShopperStillChangesTheRestOfTheirProfile(t *testing.T) {
	t.Parallel()

	var got service.UpdateCustomerInput
	svc := &stubCustomer{
		updateCustomerFn: func(_ context.Context, id string, in service.UpdateCustomerInput) (models.Customer, error) {
			got = in
			return models.Customer{ID: id}, nil
		},
	}
	r := routerWithIdentity(svc, pathProvingIdentity{})

	rec := send(t, r, http.MethodPut, "/store/v1/customers/cust_1",
		`{"first_name":"Ece","last_name":"Kaya","phone":"+90 555 000 0000","metadata":{"newsletter":true}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, got.FirstName)
	require.NotNil(t, got.LastName)
	require.NotNil(t, got.Phone)
	assert.Equal(t, "Ece", *got.FirstName)
	assert.Equal(t, "Kaya", *got.LastName)
	assert.Equal(t, "+90 555 000 0000", *got.Phone)
	assert.Equal(t, map[string]any{"newsletter": true}, got.Metadata)
	assert.Nil(t, got.Email)
}

// TestAnOperatorStillChangesTheAddress: the admin route keeps the field.
func TestAnOperatorStillChangesTheAddress(t *testing.T) {
	t.Parallel()

	var got service.UpdateCustomerInput
	svc := &stubCustomer{
		updateCustomerFn: func(_ context.Context, id string, in service.UpdateCustomerInput) (models.Customer, error) {
			got = in
			return models.Customer{ID: id}, nil
		},
	}
	r := routerWithIdentity(svc, pathProvingIdentity{})

	operator := corehttp.WithPrincipal(context.Background(), corehttp.Principal{
		ID: "user_operator", Kind: "user", Scopes: []string{corehttp.ScopeAdmin},
	})
	req := httptest.NewRequestWithContext(operator, http.MethodPut, "/admin/v1/customers/cust_1",
		strings.NewReader(`{"email":"new@example.test"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, got.Email)
	assert.Equal(t, "new@example.test", *got.Email)
}
