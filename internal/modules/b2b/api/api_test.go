package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/b2b/api"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
	"github.com/bdrtr/gobit/internal/modules/b2b/service"
)

// testNow is the tests' deterministic time source.
var testNow = time.Date(2026, time.March, 17, 12, 0, 0, 0, time.UTC)

// newTestRouter builds a router with the routes bound over the given fake
// service.
//
// The identity bound here AGREES with the path, because the tests in this file
// are about routing, scopes and body handling. The identity check itself is
// only observable when the two can DISAGREE, and that arrangement is in
// identity_test.go beside it.
func newTestRouter(svc api.B2B) chi.Router {
	r := chi.NewRouter()
	api.New(svc, boundTo(pathProvingIdentity{}), false).Routes(r)
	return r
}

// boundTo is the lookup an installation that HAS bound a verifier hands the
// handler.
//
// The lookup exists because "nothing is bound" is a third answer the
// corehttp.Identity contract cannot express; a test that wants a bound one says
// so here, and a test that wants none passes nil to api.New.
func boundTo(identity corehttp.Identity) api.IdentityLookup {
	return func(context.Context) (corehttp.Identity, error) { return identity, nil }
}

// pathProvingIdentity hands back the customer the path already claimed.
//
// It is the implementation ADR 0043 says gobit cannot detect: it satisfies the
// interface and proves nothing. It is written down rather than left implicit
// because the framework's guarantee stops at "somebody was asked", and a reader
// should meet that sentence here rather than rediscover it.
type pathProvingIdentity struct{}

var _ corehttp.Identity = pathProvingIdentity{}

func (pathProvingIdentity) CustomerID(r *http.Request) (string, error) {
	return chi.URLParam(r, "customer_id"), nil
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
// exercises.
func do(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doAs(t, r, &adminPrincipal, method, path, body)
}

// doAs runs the request with the given identity; if the identity is nil the
// request goes WITHOUT one.
func doAs(
	t *testing.T,
	r chi.Router,
	principal *corehttp.Principal,
	method, path, body string,
) *httptest.ResponseRecorder {
	t.Helper()

	ctx := context.Background()
	if principal != nil {
		ctx = corehttp.WithPrincipal(ctx, *principal)
	}
	req := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
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

// dataOf returns the object inside a single-item response envelope.
func dataOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	data, ok := decodeResponse(t, rec)["data"].(map[string]any)
	require.True(t, ok, "the response envelope has to hold an object: %s", rec.Body.String())
	return data
}

// sampleCompany is the tests' default company record.
func sampleCompany() models.Company {
	return models.Company{
		ID:                       "comp_01",
		Name:                     "Acme Sanayi A.S.",
		Email:                    "muhasebe@acme.example",
		CurrencyCode:             "TRY",
		CountryCode:              "TR",
		SpendingLimitResetPeriod: models.ResetMonthly,
		CreatedAt:                testNow,
		UpdatedAt:                testNow,
	}
}

// sampleEmployee is the tests' default employee record.
func sampleEmployee() models.CompanyEmployee {
	limit := int64(150000)
	return models.CompanyEmployee{
		ID:            "compemp_01",
		CompanyID:     "comp_01",
		CustomerID:    "cust_01",
		SpendingLimit: &limit,
		CreatedAt:     testNow,
		UpdatedAt:     testNow,
	}
}

// pathParamRe captures the {param} parts of a chi route pattern.
var pathParamRe = regexp.MustCompile(`\{([^}]*)\}`)

// routeTree returns every endpoint (method, pattern) in the router tree.
func routeTree(t *testing.T, r chi.Router) map[string][]string {
	t.Helper()

	out := map[string][]string{}
	err := chi.Walk(r, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out[pattern] = append(out[pattern], method)
		return nil
	})
	require.NoError(t, err, "the router tree could not be walked")
	return out
}

// TestStoreHasNoCompanyIDParameter pins the module's most important storefront
// invariant STRUCTURALLY.
//
// The claim is this: the storefront surface has NO endpoint that can be called
// with the id of a company (or of an employee record); the only key is the
// customer's own id. A request to "read somebody else's company" thereby
// becomes not a request that is refused but one that CANNOT BE EXPRESSED.
//
// The test keeps no LIST of endpoints, it WALKS the tree: a storefront endpoint
// added tomorrow falls under it automatically. A hand-written list would be
// blind to the first endpoint somebody forgot to add to it — and the endpoint
// that gets forgotten is exactly the newly written one.
func TestStoreHasNoCompanyIDParameter(t *testing.T) {
	tree := routeTree(t, newTestRouter(&stubB2B{}))

	var storefront []string
	for pattern := range tree {
		if strings.HasPrefix(pattern, "/store/v1") {
			storefront = append(storefront, pattern)
		}
	}
	require.NotEmpty(t, storefront, "no storefront endpoints were found; the walking logic may be broken")

	for _, pattern := range storefront {
		for _, match := range pathParamRe.FindAllStringSubmatch(pattern, -1) {
			assert.Equal(t, "customer_id", match[1],
				"the storefront endpoint %q takes a parameter OTHER THAN the customer id; "+
					"an endpoint addressed by a company or employee id would make it "+
					"possible to read somebody else's company", pattern)
		}
	}
}

// TestAdminEndpointsRequireAScope verifies, by walking the tree, that every
// /admin/v1 endpoint asks for a scope.
//
// This is the module-level counterpart of e2e/authorization_test.go: since the
// module is not wired into the composition root yet, that test does not see
// these endpoints, and forgetting to enforce a scope would stay silent.
func TestAdminEndpointsRequireAScope(t *testing.T) {
	r := newTestRouter(&stubB2B{})

	// The identity is VALID but has NO scope: the expected answer is 403, not
	// 401. The distinction is corehttp.RequireScope's contract — 401 would
	// mean "tell me who you are", and the client would retry forever with the
	// same identity.
	unscoped := corehttp.Principal{ID: "user_unscoped", Kind: "user", Scopes: []string{}}

	count := 0
	for pattern, methods := range routeTree(t, r) {
		if !strings.HasPrefix(pattern, "/admin/v1") {
			continue
		}
		path := pathParamRe.ReplaceAllString(pattern, "sahte_kimlik")
		for _, method := range methods {
			count++
			t.Run(method+" "+pattern, func(t *testing.T) {
				rec := doAs(t, r, &unscoped, method, path, "{}")
				assert.Equal(t, http.StatusForbidden, rec.Code,
					"an identity without a scope has to get 403 on this endpoint; body: %s", rec.Body.String())
			})
		}
	}
	require.Equal(t, 10, count, "the admin surface differs from what was expected; the walking logic may be broken")
}

// TestReadScopeDoesNotOpenWriteEndpoints verifies that the two scopes are
// really separate.
//
// Were they not, a read scope granted for reporting would also be enough to
// change employees' spending limits.
func TestReadScopeDoesNotOpenWriteEndpoints(t *testing.T) {
	stub := &stubB2B{
		listCompaniesFn: func(context.Context, service.ListCompaniesInput) (service.Page[models.Company], error) {
			return service.Page[models.Company]{Items: []models.Company{sampleCompany()}, Count: 1}, nil
		},
	}
	r := newTestRouter(stub)
	reader := corehttp.Principal{ID: "user_okuma", Kind: "user", Scopes: []string{api.ScopeRead}}

	rec := doAs(t, r, &reader, http.MethodGet, "/admin/v1/b2b/companies", "")
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doAs(t, r, &reader, http.MethodPut, "/admin/v1/b2b/employees/compemp_01",
		`{"spending_limit":999999}`)
	assert.Equal(t, http.StatusForbidden, rec.Code,
		"the read scope must not be enough to change a spending limit")
}

// TestStoreEndpointsRequireNoScope verifies that the store surface works with
// the publishable key: that key by definition carries no scope.
func TestStoreEndpointsRequireNoScope(t *testing.T) {
	stub := &stubB2B{
		membershipFn: func(context.Context, string) (service.Membership, error) {
			return service.Membership{Company: sampleCompany(), Employee: sampleEmployee()}, nil
		},
	}
	r := newTestRouter(stub)

	rec := doAs(t, r, nil, http.MethodGet,
		"/store/v1/b2b/customers/cust_01/company", "")
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// TestStoreReadsTheCustomerFromThePath verifies that the handler hands the
// service the RIGHT customer.
//
// Reading the identity in a single place (storeCustomerID) is so that, when a
// customer session arrives, the change can be made in a single file.
func TestStoreReadsTheCustomerFromThePath(t *testing.T) {
	stub := &stubB2B{
		membershipFn: func(_ context.Context, customerID string) (service.Membership, error) {
			if customerID != "cust_42" {
				return service.Membership{}, errors.NotFound("b2b_employee_not_found", "missing")
			}
			window := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
			return service.Membership{
				Company:             sampleCompany(),
				Employee:            sampleEmployee(),
				SpendingWindowStart: &window,
			}, nil
		},
	}
	r := newTestRouter(stub)

	rec := doAs(t, r, nil, http.MethodGet,
		"/store/v1/b2b/customers/cust_42/employee", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "cust_42", stub.lastCustomerID)

	data := dataOf(t, rec)
	assert.Equal(t, "compemp_01", data["id"])
	assert.InEpsilon(t, float64(150000), data["spending_limit"], 0)
	assert.Equal(t, string(models.ResetMonthly), data["spending_limit_reset_period"])
	assert.Equal(t, "2026-03-01T00:00:00Z", data["spending_window_start"])

	// The REMAINING allowance field is DELIBERATELY absent; a made-up number
	// would misinform the client (see service.Membership).
	assert.NotContains(t, data, "spending_remaining")
}

// TestStoreAnswersNotFoundForANonMember verifies that a customer without a
// company gets a 404, not an empty record.
func TestStoreAnswersNotFoundForANonMember(t *testing.T) {
	stub := &stubB2B{
		membershipFn: func(context.Context, string) (service.Membership, error) {
			return service.Membership{}, errors.NotFound("b2b_employee_not_found",
				"the customer is not an employee of any company")
		},
	}
	r := newTestRouter(stub)

	for _, path := range []string{
		"/store/v1/b2b/customers/cust_01/company",
		"/store/v1/b2b/customers/cust_01/employee",
	} {
		rec := doAs(t, r, nil, http.MethodGet, path, "")
		assert.Equal(t, http.StatusNotFound, rec.Code, "%s: %s", path, rec.Body.String())
	}
}

// TestErrorKindBecomesStatusCode verifies that the handler does NOT CHOOSE the
// status code: the classification comes from the service and the core turns
// it into a code.
func TestErrorKindBecomesStatusCode(t *testing.T) {
	cases := map[string]struct {
		err  error
		want int
	}{
		"not found": {errors.NotFound("b2b_company_not_found", "missing"), http.StatusNotFound},
		"invalid":   {errors.Invalid("b2b_invalid_input", "bad"), http.StatusUnprocessableEntity},
		"conflict": {
			errors.Conflict("b2b_link_failed", "the customer is already at another company"),
			http.StatusConflict,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			stub := &stubB2B{
				getCompanyFn: func(context.Context, string) (models.Company, error) {
					return models.Company{}, tc.err
				},
			}
			rec := do(t, newTestRouter(stub), http.MethodGet, "/admin/v1/b2b/companies/comp_01", "")
			assert.Equal(t, tc.want, rec.Code, rec.Body.String())
		})
	}
}

// TestCreateCompanyBodyReachesTheService verifies that the fields are mapped
// correctly.
func TestCreateCompanyBodyReachesTheService(t *testing.T) {
	stub := &stubB2B{
		createCompanyFn: func(_ context.Context, in service.CompanyInput) (models.Company, error) {
			company := sampleCompany()
			company.Name = in.Name
			return company, nil
		},
	}
	r := newTestRouter(stub)

	rec := do(t, r, http.MethodPost, "/admin/v1/b2b/companies", `{
        "name": "Acme Sanayi A.S.",
        "email": "muhasebe@acme.example",
        "phone": "",
        "address": "",
        "city": "",
        "postal_code": "",
        "country_code": "TR",
        "currency_code": "TRY",
        "spending_limit_reset_period": "monthly"
    }`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	assert.Equal(t, "TRY", stub.lastCompanyInput.CurrencyCode)
	assert.Equal(t, "monthly", stub.lastCompanyInput.SpendingLimitResetPeriod)
	assert.Equal(t, "comp_01", dataOf(t, rec)["id"])
}

// TestUnknownFieldIsRejected keeps a silently ignored field from giving the
// client the impression that it was "written".
//
// In this module that field could be a spending limit: had a misspelled key
// been silently dropped, an employee who stayed unlimited would be believed to
// be limited.
func TestUnknownFieldIsRejected(t *testing.T) {
	r := newTestRouter(&stubB2B{})

	rec := do(t, r, http.MethodPut, "/admin/v1/b2b/employees/compemp_01",
		`{"spendingLimit": 100}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestClearLimitFlagReachesTheService verifies that the flag added because JSON
// cannot tell null from absent is really carried through.
func TestClearLimitFlagReachesTheService(t *testing.T) {
	stub := &stubB2B{
		updateEmployeeFn: func(context.Context, string, service.UpdateEmployeeInput) (models.CompanyEmployee, error) {
			employee := sampleEmployee()
			employee.SpendingLimit = nil
			return employee, nil
		},
	}
	r := newTestRouter(stub)

	rec := do(t, r, http.MethodPut, "/admin/v1/b2b/employees/compemp_01",
		`{"clear_spending_limit": true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	assert.True(t, stub.lastEmployeeUpdate.ClearSpendingLimit)
	assert.Nil(t, stub.lastEmployeeUpdate.SpendingLimit)
	assert.Nil(t, dataOf(t, rec)["spending_limit"], "an unlimited employee's limit has to come back as null")
}

// TestEmployeeFiltersAreReadFromTheQueryString pins the parameters the handler
// really reads; a filter that is not read would be a promise that stands in
// the document but does not work.
func TestEmployeeFiltersAreReadFromTheQueryString(t *testing.T) {
	stub := &stubB2B{
		listEmployeesFn: func(context.Context, service.ListEmployeesInput) (service.Page[models.CompanyEmployee], error) {
			return service.Page[models.CompanyEmployee]{
				Items: []models.CompanyEmployee{sampleEmployee()}, Count: 1, Limit: 10, Offset: 0,
			}, nil
		},
	}
	r := newTestRouter(stub)

	rec := do(t, r, http.MethodGet,
		"/admin/v1/b2b/employees?company_id=comp_01&is_company_admin=true&limit=10&offset=0", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.NotNil(t, stub.lastEmployeeList.CompanyID)
	assert.Equal(t, "comp_01", *stub.lastEmployeeList.CompanyID)
	require.NotNil(t, stub.lastEmployeeList.IsCompanyAdmin)
	assert.True(t, *stub.lastEmployeeList.IsCompanyAdmin)
	assert.Equal(t, int64(10), stub.lastEmployeeList.Limit)

	envelope := decodeResponse(t, rec)
	assert.InEpsilon(t, float64(1), envelope["count"], 0)
	assert.Contains(t, envelope, "limit")
	assert.Contains(t, envelope, "offset")
}

// TestNonNumericPagingIsRejected keeps the request from silently falling back
// to the first page.
func TestNonNumericPagingIsRejected(t *testing.T) {
	r := newTestRouter(&stubB2B{})

	rec := do(t, r, http.MethodGet, "/admin/v1/b2b/companies?limit=abc", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())

	rec = do(t, r, http.MethodGet, "/admin/v1/b2b/employees?is_company_admin=belki", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestDeleteAnswersWithoutABody verifies that the 204 really has no body.
func TestDeleteAnswersWithoutABody(t *testing.T) {
	stub := &stubB2B{
		deleteCompanyFn:  func(context.Context, string) error { return nil },
		deleteEmployeeFn: func(context.Context, string) error { return nil },
	}
	r := newTestRouter(stub)

	rec := do(t, r, http.MethodDelete, "/admin/v1/b2b/companies/comp_01", "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())

	rec = do(t, r, http.MethodDelete, "/admin/v1/b2b/employees/compemp_01", "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
}
