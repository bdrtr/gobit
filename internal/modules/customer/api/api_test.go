package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/customer/api"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// testNow is the tests' deterministic time source.
var testNow = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

// newTestRouter builds a router with the routes bound over the given fake
// service.
//
// The bound identity is [pathProvingIdentity], which proves whatever the path
// claims. That is the WEAKEST legal implementation of corehttp.Identity and it
// is chosen on purpose: it keeps the tests in this file about what they were
// always about — routing, body decoding, envelope shape and the status a typed
// error turns into — instead of turning every one of them into a test of the
// identity gate. The gate itself is exercised in identity_test.go, where the
// identity proves a FIXED customer and can therefore disagree with the path.
//
// It is also, deliberately, the implementation ADR 0043 says the framework
// cannot detect: an embedder that hands the path parameter back has changed
// nothing, and gobit still refuses only when NOBODY has been asked.
func newTestRouter(svc api.Customer) chi.Router {
	return routerWithIdentity(svc, pathProvingIdentity{})
}

// adminPrincipal is the tests' default caller: a fully privileged admin
// identity.
var adminPrincipal = corehttp.Principal{
	ID:     "user_test",
	Kind:   "user",
	Scopes: []string{corehttp.ScopeAdmin},
}

// do runs an HTTP request with a FULLY PRIVILEGED identity and returns the
// response.
//
// Putting the identity on the context is needed because the admin endpoints
// are protected with corehttp.RequireScope: that middleware reads the identity
// from the context, and corehttp.RequireAdmin, which puts it there, is ABSENT
// from this test (the router is built directly). Without the identity every
// admin test in this file would get a 401 before reaching the behavior it
// exercises. What the tests verify did not change; only who the caller is was
// spelled out.
func do(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doAs(t, r, &adminPrincipal, method, path, body)
}

// doAs runs the request with the given identity; if the identity is nil the
// request goes WITHOUT one.
func doAs(t *testing.T, r chi.Router, principal *corehttp.Principal, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	ctx := context.Background()
	if principal != nil {
		ctx = corehttp.WithPrincipal(ctx, *principal)
	}
	req := httptest.NewRequestWithContext(ctx, method, path, reader)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// decodeResponse decodes the response body into a map.
func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "body: %s", rec.Body.String())
	return out
}

// sampleCustomer is the sample customer the tests return.
func sampleCustomer(hasAccount bool) models.Customer {
	return models.Customer{
		ID:         "cust_0123456789ABCDEFGHJKMNPQRS",
		Email:      "ali@example.com",
		FirstName:  "Ali",
		LastName:   "Veli",
		HasAccount: hasAccount,
		CreatedAt:  testNow,
		UpdatedAt:  testNow,
	}
}

// TestAdminCreateCustomerOpensAnAccount proves that the admin endpoint opens a
// REGISTERED account.
//
// Had the distinction been left to a flag in the body, an admin request would
// silently have fallen outside the uniqueness rule.
func TestAdminCreateCustomerOpensAnAccount(t *testing.T) {
	svc := &stubCustomer{
		createCustomerFn: func(_ context.Context, _ service.CustomerInput) (models.Customer, error) {
			return sampleCustomer(true), nil
		},
	}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost, "/admin/v1/customers", `{"email":"Ali@Example.com"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	data, ok := decodeResponse(t, rec)["data"].(map[string]any)
	require.True(t, ok, "a single response has to be in the {\"data\":{...}} envelope")
	assert.Equal(t, true, data["has_account"])
	assert.Equal(t, "Ali@Example.com", svc.lastInput.Email, "the body has to reach the service as it is")
}

// TestStoreRegistrationOpensAGuest proves that the store endpoint opens a
// GUEST record.
func TestStoreRegistrationOpensAGuest(t *testing.T) {
	var called bool
	svc := &stubCustomer{
		registerGuestFn: func(_ context.Context, _ service.CustomerInput) (models.Customer, error) {
			called = true
			return sampleCustomer(false), nil
		},
		createCustomerFn: func(_ context.Context, _ service.CustomerInput) (models.Customer, error) {
			t.Fatal("the storefront endpoint must NOT CALL the path that opens an account")
			return models.Customer{}, nil
		},
	}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost, "/store/v1/customers", `{"email":"guest@example.com"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.True(t, called)

	data, _ := decodeResponse(t, rec)["data"].(map[string]any)
	assert.Equal(t, false, data["has_account"])
}

// TestErrorKindBecomesStatusCode proves that the handler does NOT CHOOSE the
// status code and that the error kind is translated.
//
// The mapping lives in core/http (plan Section 2.7); were it repeated in the
// handler, the two places could drift apart.
func TestErrorKindBecomesStatusCode(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
	}{
		"not found": {errors.NotFound("customer_not_found", "missing"), http.StatusNotFound},
		"invalid":   {errors.Invalid("customer_invalid_input", "bad"), http.StatusUnprocessableEntity},
		"conflict":  {errors.Conflict("customer_email_taken", "taken"), http.StatusConflict},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc := &stubCustomer{
				getCustomerFn: func(_ context.Context, _ string) (models.Customer, error) {
					return models.Customer{}, tc.err
				},
			}
			r := newTestRouter(svc)

			rec := do(t, r, http.MethodGet, "/admin/v1/customers/cust_1", "")
			assert.Equal(t, tc.status, rec.Code, rec.Body.String())

			errBody, ok := decodeResponse(t, rec)["error"].(map[string]any)
			require.True(t, ok, "the error envelope has to be {\"error\":{...}}")
			assert.Equal(t, errors.CodeOf(tc.err), errBody["code"])
		})
	}
}

// TestUnknownFieldIsRejected proves that no field is silently ignored.
//
// A silently skipped field means a value the client believes it sent is never
// written.
func TestUnknownFieldIsRejected(t *testing.T) {
	svc := &stubCustomer{
		createCustomerFn: func(_ context.Context, _ service.CustomerInput) (models.Customer, error) {
			t.Fatal("a body carrying an unknown field must NOT REACH the service")
			return models.Customer{}, nil
		},
	}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost, "/admin/v1/customers",
		`{"email":"ali@example.com","has_account":true}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestEmptyAndDoubleBody proves that malformed bodies are rejected.
func TestEmptyAndDoubleBody(t *testing.T) {
	r := newTestRouter(&stubCustomer{})

	rec := do(t, r, http.MethodPost, "/admin/v1/customers", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "an empty body has to be rejected")

	rec = do(t, r, http.MethodPost, "/admin/v1/customers",
		`{"email":"a@b.co"}{"email":"c@d.co"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"a single JSON document is expected; the second one must not be silently ignored")
}

// TestListEnvelope proves the paging fields of the list response.
func TestListEnvelope(t *testing.T) {
	svc := &stubCustomer{
		listCustomersFn: func(_ context.Context, in service.ListCustomersInput) (service.Page[models.Customer], error) {
			return service.Page[models.Customer]{
				Items:  []models.Customer{sampleCustomer(true)},
				Count:  42,
				Limit:  in.Limit,
				Offset: in.Offset,
			}, nil
		},
	}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodGet, "/admin/v1/customers?limit=10&offset=20", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	body := decodeResponse(t, rec)
	assert.Equal(t, float64(42), body["count"])
	assert.Equal(t, float64(10), body["limit"])
	assert.Equal(t, float64(20), body["offset"])
	items, ok := body["data"].([]any)
	require.True(t, ok, "a list response has to be in the {\"data\":[...]} envelope")
	assert.Len(t, items, 1)
}

// TestListFiltersReachTheService proves that the query parameters are read
// correctly.
//
// For has_account nil and false are DIFFERENT: not giving the parameter at all
// means not filtering, giving "false" means filtering for guests.
func TestListFiltersReachTheService(t *testing.T) {
	svc := &stubCustomer{
		listCustomersFn: func(_ context.Context, _ service.ListCustomersInput) (service.Page[models.Customer], error) {
			return service.Page[models.Customer]{Items: []models.Customer{}}, nil
		},
	}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodGet, "/admin/v1/customers", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Nil(t, svc.lastListInput.HasAccount, "no filter must be set up when the parameter was not given")
	assert.Nil(t, svc.lastListInput.Email)

	rec = do(t, r, http.MethodGet, "/admin/v1/customers?has_account=false&email=A%40B.co", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, svc.lastListInput.HasAccount)
	assert.False(t, *svc.lastListInput.HasAccount, "false is a real filter")
	require.NotNil(t, svc.lastListInput.Email)
	assert.Equal(t, "A@B.co", *svc.lastListInput.Email)
}

// TestMalformedQueryParameter proves that a limit that cannot be converted to a
// number does not silently fall back to zero.
func TestMalformedQueryParameter(t *testing.T) {
	svc := &stubCustomer{
		listCustomersFn: func(_ context.Context, _ service.ListCustomersInput) (service.Page[models.Customer], error) {
			t.Fatal("a malformed parameter must not reach the service")
			return service.Page[models.Customer]{}, nil
		},
	}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodGet, "/admin/v1/customers?limit=abc", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	rec = do(t, r, http.MethodGet, "/admin/v1/customers?has_account=maybe", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestGuestConversionReturnsTheCurrentRecord proves that the record is read
// again after the conversion.
func TestGuestConversionReturnsTheCurrentRecord(t *testing.T) {
	var converted bool
	svc := &stubCustomer{
		convertGuestFn: func(_ context.Context, _ string) error {
			converted = true
			return nil
		},
		getCustomerFn: func(_ context.Context, _ string) (models.Customer, error) {
			return sampleCustomer(converted), nil
		},
	}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost, "/admin/v1/customers/cust_1/convert-to-account", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	data, _ := decodeResponse(t, rec)["data"].(map[string]any)
	assert.Equal(t, true, data["has_account"], "the response has to show the state AFTER the conversion")
}

// TestGuestConversionConflict proves that a conflict is translated into a 409.
func TestGuestConversionConflict(t *testing.T) {
	svc := &stubCustomer{
		convertGuestFn: func(_ context.Context, _ string) error {
			return errors.Conflict("customer_email_taken", "taken")
		},
		getCustomerFn: func(_ context.Context, _ string) (models.Customer, error) {
			t.Fatal("the record must NOT BE READ again when the conversion failed")
			return models.Customer{}, nil
		},
	}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost, "/admin/v1/customers/cust_1/convert-to-account", "")
	assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
}

// TestDeleteReturnsNoContent proves that the delete endpoints return 204.
func TestDeleteReturnsNoContent(t *testing.T) {
	svc := &stubCustomer{
		deleteCustomerFn: func(_ context.Context, _ string) error { return nil },
	}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodDelete, "/admin/v1/customers/cust_1", "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
}

// TestRemoveFromGroupPathParameters proves that the path parameters are not
// mixed up.
//
// The path has the shape /customer-groups/{id}/customers/{customer_id}; had the
// two swapped places, the request would have turned into an attempt to delete a
// membership that does not exist.
func TestRemoveFromGroupPathParameters(t *testing.T) {
	svc := &stubCustomer{
		removeFromGroupFn: func(_ context.Context, _, _ string) error { return nil },
	}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodDelete,
		"/admin/v1/customer-groups/custgrp_9/customers/cust_7", "")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	assert.Equal(t, "cust_7", svc.lastCustomerID)
	assert.Equal(t, "custgrp_9", svc.lastGroupID)
}

// TestUnpagedListEnvelope proves that unpaged lists use the same envelope shape
// too.
func TestUnpagedListEnvelope(t *testing.T) {
	svc := &stubCustomer{
		listAddressesFn: func(_ context.Context, _ string) ([]models.CustomerAddress, error) {
			return []models.CustomerAddress{
				{ID: "addr_1", CustomerID: "cust_1", Address1: "Street 1", City: "Istanbul", CountryCode: "TR"},
				{ID: "addr_2", CustomerID: "cust_1", Address1: "Street 2", City: "Ankara", CountryCode: "TR"},
			}, nil
		},
	}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodGet, "/admin/v1/customers/cust_1/addresses", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	body := decodeResponse(t, rec)
	assert.Equal(t, float64(2), body["count"])
	assert.Equal(t, float64(2), body["limit"], "with no pages the limit equals the record count")
	assert.Equal(t, float64(0), body["offset"])
}

// TestEmptyListIsAnArrayNotNull proves that an empty list appears as [] in the
// JSON.
func TestEmptyListIsAnArrayNotNull(t *testing.T) {
	svc := &stubCustomer{
		listAddressesFn: func(_ context.Context, _ string) ([]models.CustomerAddress, error) {
			return nil, nil
		},
	}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodGet, "/admin/v1/customers/cust_1/addresses", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"data":[]`, "an empty list has to be [], not null")
}

// TestStoreAddressEndpoints proves that the storefront endpoints take the
// customer id from the path.
//
// The path is the CLAIM and it still decides which address book is opened; what
// ADR 0043 added is that the claim has to be backed, and the router built here
// binds an identity that backs it (see [newTestRouter]). The refusals themselves
// are in identity_test.go, where the identity can disagree with the path.
func TestStoreAddressEndpoints(t *testing.T) {
	svc := &stubCustomer{
		createAddressFn: func(_ context.Context, _ string, in service.AddressInput) (models.CustomerAddress, error) {
			return models.CustomerAddress{
				ID: "addr_1", CustomerID: "cust_1",
				Address1: in.Address1, City: in.City, CountryCode: "TR",
			}, nil
		},
		setDefaultShipFn: func(_ context.Context, _, addressID string) (models.CustomerAddress, error) {
			return models.CustomerAddress{ID: addressID, CustomerID: "cust_1", IsDefaultShipping: true}, nil
		},
		setDefaultBillFn: func(_ context.Context, _, addressID string) (models.CustomerAddress, error) {
			return models.CustomerAddress{ID: addressID, CustomerID: "cust_1", IsDefaultBilling: true}, nil
		},
	}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost, "/store/v1/customers/cust_1/addresses",
		`{"address_1":"Street 1","city":"Istanbul","country_code":"tr"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "cust_1", svc.lastCustomerID)

	rec = do(t, r, http.MethodPost,
		"/store/v1/customers/cust_1/addresses/addr_1/default-shipping", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ := decodeResponse(t, rec)["data"].(map[string]any)
	assert.Equal(t, true, data["is_default_shipping"])
	assert.Equal(t, "addr_1", svc.lastAddressID)

	rec = do(t, r, http.MethodPost,
		"/store/v1/customers/cust_1/addresses/addr_1/default-billing", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ = decodeResponse(t, rec)["data"].(map[string]any)
	assert.Equal(t, true, data["is_default_billing"])
}

// TestAdminDefaultAddressEndpoints proves that the endpoints that set the
// default address exist on the ADMIN side too.
//
// Without them an operator would have to RE-CREATE an existing address to make
// it the default: the update body deliberately carries no flag, and re-creating
// the address would change its identifier.
func TestAdminDefaultAddressEndpoints(t *testing.T) {
	svc := &stubCustomer{
		setDefaultShipFn: func(_ context.Context, _, addressID string) (models.CustomerAddress, error) {
			return models.CustomerAddress{ID: addressID, CustomerID: "cust_1", IsDefaultShipping: true}, nil
		},
		setDefaultBillFn: func(_ context.Context, _, addressID string) (models.CustomerAddress, error) {
			return models.CustomerAddress{ID: addressID, CustomerID: "cust_1", IsDefaultBilling: true}, nil
		},
	}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost,
		"/admin/v1/customers/cust_1/addresses/addr_1/default-shipping", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ := decodeResponse(t, rec)["data"].(map[string]any)
	assert.Equal(t, true, data["is_default_shipping"])
	assert.Equal(t, "cust_1", svc.lastCustomerID, "the customer id has to come from the path parameter")
	assert.Equal(t, "addr_1", svc.lastAddressID)

	rec = do(t, r, http.MethodPost,
		"/admin/v1/customers/cust_1/addresses/addr_1/default-billing", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ = decodeResponse(t, rec)["data"].(map[string]any)
	assert.Equal(t, true, data["is_default_billing"])
	assert.Equal(t, "cust_1", svc.lastCustomerID)
}

// TestAdminGroupUpdateAndDelete proves the endpoints that correct and delete a
// group.
//
// The name is unique among live groups; without these endpoints a mistyped
// name could neither be corrected nor released.
func TestAdminGroupUpdateAndDelete(t *testing.T) {
	var got service.UpdateGroupInput
	svc := &stubCustomer{
		updateGroupFn: func(_ context.Context, id string, in service.UpdateGroupInput) (models.CustomerGroup, error) {
			got = in
			name := "VIP"
			if in.Name != nil {
				name = *in.Name
			}
			return models.CustomerGroup{
				ID: id, Name: name, Metadata: in.Metadata,
				CreatedAt: testNow, UpdatedAt: testNow,
			}, nil
		},
		deleteGroupFn: func(_ context.Context, _ string) error { return nil },
	}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPut, "/admin/v1/customer-groups/custgrp_1", `{"name":"B2B"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ := decodeResponse(t, rec)["data"].(map[string]any)
	assert.Equal(t, "B2B", data["name"])
	assert.Equal(t, "custgrp_1", svc.lastGroupID)

	// A name that is not sent reaches the service as nil: "leave it alone" is
	// kept apart from "empty it".
	rec = do(t, r, http.MethodPut, "/admin/v1/customer-groups/custgrp_1",
		`{"metadata":{"discount":"10"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Nil(t, got.Name, "a name that was not sent has to reach the service as nil")
	assert.Equal(t, "10", got.Metadata["discount"])

	rec = do(t, r, http.MethodDelete, "/admin/v1/customer-groups/custgrp_1", "")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Empty(t, rec.Body.String(), "a 204 has to come back with an empty body")
	assert.Equal(t, "custgrp_1", svc.lastGroupID)
}

// TestAddressUpdateCarriesNoDefaultFlag proves that the body does NOT ACCEPT the
// default flag.
//
// Changing the flag concerns the customer's other addresses too; done as a
// single-row update, it could leave the customer with two default addresses.
func TestAddressUpdateCarriesNoDefaultFlag(t *testing.T) {
	svc := &stubCustomer{
		updateAddressFn: func(_ context.Context, _, _ string, _ service.UpdateAddressInput) (models.CustomerAddress, error) {
			t.Fatal("a body carrying the default flag must NOT REACH the service")
			return models.CustomerAddress{}, nil
		},
	}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPut, "/store/v1/customers/cust_1/addresses/addr_1",
		`{"city":"Ankara","is_default_shipping":true}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestStoreHasNoCustomerListing proves that the customer listing does not exist
// on the store side.
//
// A customer listing opened on the storefront would make every customer's
// e-mail address public.
func TestStoreHasNoCustomerListing(t *testing.T) {
	r := newTestRouter(&stubCustomer{})

	rec := do(t, r, http.MethodGet, "/store/v1/customers", "")
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
		"the storefront must have no customer LISTING endpoint")
}

// TestNarrowScopeDoesNotOpenWriteEndpoints proves that an identity carrying only
// [api.ScopeRead] cannot get through the admin WRITE endpoints.
//
// The scenario under test is concrete: an admin identity signed in with the
// read scope could, were the scope not enforced, delete customer records with
// DELETE /admin/v1/customers/{id}. The service must NOT be called AT ALL; the
// t.Fatal inside the fake proves that separately.
func TestNarrowScopeDoesNotOpenWriteEndpoints(t *testing.T) {
	svc := &stubCustomer{
		createCustomerFn: func(_ context.Context, _ service.CustomerInput) (models.Customer, error) {
			t.Fatal("an unauthorized request must NOT REACH the service")
			return models.Customer{}, nil
		},
		deleteCustomerFn: func(_ context.Context, _ string) error {
			t.Fatal("an unauthorized request must NOT REACH the service")
			return nil
		},
		updateCustomerFn: func(_ context.Context, _ string, _ service.UpdateCustomerInput) (models.Customer, error) {
			t.Fatal("an unauthorized request must NOT REACH the service")
			return models.Customer{}, nil
		},
	}
	r := newTestRouter(svc)
	narrow := corehttp.Principal{ID: "user_narrow", Kind: "user", Scopes: []string{api.ScopeRead}}

	for _, endpoint := range []struct{ method, path, body string }{
		{http.MethodPost, "/admin/v1/customers", `{"email":"a@b.co"}`},
		{http.MethodPut, "/admin/v1/customers/cust_1", `{"first_name":"Ali"}`},
		{http.MethodDelete, "/admin/v1/customers/cust_1", ""},
		{http.MethodPost, "/admin/v1/customer-groups", `{"name":"VIP"}`},
	} {
		rec := doAs(t, r, &narrow, endpoint.method, endpoint.path, endpoint.body)
		assert.Equal(t, http.StatusForbidden, rec.Code,
			"%s %s must not open with the read scope: %s", endpoint.method, endpoint.path, rec.Body.String())
	}
}

// TestNarrowScopePassesOnReadEndpoints proves that the same narrow identity DOES
// GET THROUGH the admin READ endpoints.
//
// The value of enforcing scopes lies in it really accepting a narrow scope as
// well: were it only to refuse, nobody would hand out narrow scopes and
// everyone would be given admin.
func TestNarrowScopePassesOnReadEndpoints(t *testing.T) {
	svc := &stubCustomer{
		listCustomersFn: func(_ context.Context, _ service.ListCustomersInput) (service.Page[models.Customer], error) {
			return service.Page[models.Customer]{Items: []models.Customer{sampleCustomer(true)}, Count: 1}, nil
		},
		getCustomerFn: func(_ context.Context, _ string) (models.Customer, error) {
			return sampleCustomer(true), nil
		},
	}
	r := newTestRouter(svc)
	narrow := corehttp.Principal{ID: "user_narrow", Kind: "user", Scopes: []string{api.ScopeRead}}

	for _, path := range []string{"/admin/v1/customers", "/admin/v1/customers/cust_1"} {
		rec := doAs(t, r, &narrow, http.MethodGet, path, "")
		assert.Equal(t, http.StatusOK, rec.Code, "GET %s: %s", path, rec.Body.String())
	}
}

// TestAdminRequestWithoutPrincipalReturns401 proves that a request with no
// identity at all gets a 401.
//
// The distinction is deliberate: 401 means "tell me who you are", 403 means "I
// know who you are but you lack the scope". Were the two mixed up, the client
// would try refreshing its session for a problem that renewing its identity
// will not solve.
func TestAdminRequestWithoutPrincipalReturns401(t *testing.T) {
	r := newTestRouter(&stubCustomer{})

	rec := doAs(t, r, nil, http.MethodGet, "/admin/v1/customers", "")

	assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
}

// TestStoreEndpointsRequireNoScope proves that NO scope was ADDED to the store
// surface.
//
// The storefront surface's identity is the publishable key, and that key by
// definition carries no scope; should a scope be attached to the store
// endpoints by mistake, the storefront stops working altogether, and this test
// catches that at once.
func TestStoreEndpointsRequireNoScope(t *testing.T) {
	svc := &stubCustomer{
		registerGuestFn: func(_ context.Context, _ service.CustomerInput) (models.Customer, error) {
			return sampleCustomer(false), nil
		},
	}
	r := newTestRouter(svc)

	rec := doAs(t, r, nil, http.MethodPost, "/store/v1/customers", `{"email":"a@b.co"}`)

	assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

// TestStoreProfileIsTheCustomerInThePath proves that the storefront profile
// read recognizes the customer FROM THE PATH PARAMETER.
//
// The boundary ADR 0008 drew stands exactly here: gobit does not verify the
// customer's identity, the embedding application binds its own verifier and
// gobit compares it with the value in the path. Had the bound point and the
// value the handler REALLY uses drifted apart — had the handler read the
// identity from somewhere else — that comparison would be an ornament
// protecting nothing: the request would still return the name and e-mail
// address of the person in the path, and nobody would see the difference.
//
// The request is sent WITHOUT A PRINCIPAL and that is still correct: the store
// surface asks for NO scope (see TestStoreEndpointsRequireNoScope). What is
// asked for is something else — the customer's identity — and that is bound;
// were it not bound, this request would get a 401 (identity_test.go).
func TestStoreProfileIsTheCustomerInThePath(t *testing.T) {
	svc := &stubCustomer{
		getCustomerFn: func(_ context.Context, id string) (models.Customer, error) {
			customer := sampleCustomer(false)
			customer.ID = id
			return customer, nil
		},
	}
	r := newTestRouter(svc)

	rec := doAs(t, r, nil, http.MethodGet, "/store/v1/customers/cust_7", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	assert.Equal(t, "cust_7", svc.lastCustomerID,
		"the customer read has to be the identifier IN THE PATH; had the handler read it from another source, "+
			"the embedding application's comparison with the session would stop no request")

	data, ok := decodeResponse(t, rec)["data"].(map[string]any)
	require.True(t, ok, "a single response has to be in the {\"data\":{...}} envelope")
	assert.Equal(t, "cust_7", data["id"])
	assert.Equal(t, "ali@example.com", data["email"],
		"the storefront profile carries the person's e-mail address; that is what the surface opens")
}

// TestStoreProfileUpdateLeavesUnsentFieldsAlone proves that the storefront
// update passes on to the service only the fields that were SENT.
//
// The fields in the body are pointers, and the distinction here is a real
// question of losing or keeping data: a customer correcting their phone does
// not send their name. Had an unsent field reached the service as an empty
// string, that request would have ERASED the customer's first name, last name
// and e-mail address — and the client would not even know it had, because
// those fields were never in the body it sent.
//
// The same request shows that the customer updated is the identifier in the
// path too; this endpoint is a WRITE, not a READ, and it is the write side of
// the ADR 0008 boundary.
func TestStoreProfileUpdateLeavesUnsentFieldsAlone(t *testing.T) {
	var got service.UpdateCustomerInput
	svc := &stubCustomer{
		updateCustomerFn: func(_ context.Context, id string, in service.UpdateCustomerInput) (models.Customer, error) {
			got = in
			customer := sampleCustomer(false)
			customer.ID = id
			if in.Phone != nil {
				customer.Phone = *in.Phone
			}
			return customer, nil
		},
	}
	r := newTestRouter(svc)

	rec := doAs(t, r, nil, http.MethodPut, "/store/v1/customers/cust_7",
		`{"phone":"+905550000000"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "cust_7", svc.lastCustomerID, "the customer updated has to be the identifier in the path")

	require.NotNil(t, got.Phone, "a field that was sent has to REACH the service")
	assert.Equal(t, "+905550000000", *got.Phone)
	assert.Nil(t, got.FirstName,
		"a first name that was not sent has to reach the service as nil; "+
			"as an empty string it would erase the customer's name")
	assert.Nil(t, got.LastName, "a last name that was not sent has to reach it as nil too")
	assert.Nil(t, got.Email,
		"an e-mail address that was not sent has to reach it as nil; "+
			"an emptied e-mail address would destroy the account's identity")

	data, _ := decodeResponse(t, rec)["data"].(map[string]any)
	assert.Equal(t, "+905550000000", data["phone"], "the response has to show the state AFTER the update")
}

// TestStoreAddressListBelongsToTheCustomerInThePath proves that the storefront
// address list asks for the addresses of the customer IN THE PATH.
//
// The address book is where a person lives. Had the list body not passed the
// customer's identifier on to the service, or passed on some other value, two
// customers' address books would have been mixed up: one shopper would see
// somebody else's full address in their own account.
func TestStoreAddressListBelongsToTheCustomerInThePath(t *testing.T) {
	svc := &stubCustomer{
		listAddressesFn: func(_ context.Context, customerID string) ([]models.CustomerAddress, error) {
			return []models.CustomerAddress{
				{ID: "addr_1", CustomerID: customerID, Address1: "Street 1", City: "Istanbul", CountryCode: "TR"},
			}, nil
		},
	}
	r := newTestRouter(svc)

	rec := doAs(t, r, nil, http.MethodGet, "/store/v1/customers/cust_7/addresses", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	assert.Equal(t, "cust_7", svc.lastCustomerID,
		"the customer whose addresses are asked for has to be the identifier in the path")

	items, ok := decodeResponse(t, rec)["data"].([]any)
	require.True(t, ok, "a list response has to be in the {\"data\":[...]} envelope")
	require.Len(t, items, 1)
	address, ok := items[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "cust_7", address["customer_id"], "the address returned has to be the requested customer's")
}

// TestStoreAddressDeleteCarriesTheOwnerCheck proves that the delete request
// passes BOTH the customer's AND the address's identifier on to the service.
//
// The address delete query finds the address NOT by its id alone but by its id
// together with its OWNER (see repository.Repo.DeleteAddress). Had the handler
// not passed the customer's identifier on, the condition would be built with an
// empty string and the query would find no row; in the worse case — had the
// ownership condition dropped out of the query entirely — anyone who knew an
// address's identifier could delete SOMEBODY ELSE'S address. ADR 0043 made that
// guess insufficient on its own, but it did not make the ownership condition
// UNNECESSARY: the two checks are in two separate layers, and this one is the
// layer that stops a request carrying somebody else's address id even when the
// identity is right.
func TestStoreAddressDeleteCarriesTheOwnerCheck(t *testing.T) {
	var deleted [2]string
	svc := &stubCustomer{
		deleteAddressFn: func(_ context.Context, customerID, addressID string) error {
			deleted = [2]string{customerID, addressID}
			return nil
		},
	}
	r := newTestRouter(svc)

	rec := doAs(t, r, nil, http.MethodDelete,
		"/store/v1/customers/cust_7/addresses/addr_3", "")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Empty(t, rec.Body.String(), "a 204 has to come back with an empty body")

	assert.Equal(t, [2]string{"cust_7", "addr_3"}, deleted,
		"the delete request has to carry both the customer and the address; "+
			"without the customer the query loses its ownership condition")
}

// TestStoreHasNoCustomerDeleteEndpoint proves that the storefront has NO
// customer delete endpoint.
//
// Were the delete endpoint open here as well, a customer able to prove their
// identity could close their account with a single request — unlike reading a
// profile, a harm whose undoing is operator work. ADR 0043 does not make this
// endpoint's absence unnecessary: the identity check answers the question "is
// this person that person", not "may this person do this". Deleting is
// deliberately on the admin side only, and there it asks for [ScopeWrite].
func TestStoreHasNoCustomerDeleteEndpoint(t *testing.T) {
	svc := &stubCustomer{
		deleteCustomerFn: func(_ context.Context, _ string) error {
			t.Fatal("the storefront must have no customer DELETE endpoint")
			return nil
		},
	}
	r := newTestRouter(svc)

	rec := doAs(t, r, nil, http.MethodDelete, "/store/v1/customers/cust_7", "")

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
		"the storefront must have no customer delete endpoint: %s", rec.Body.String())
}
