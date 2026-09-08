package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/customer/api"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// This file holds ADR 0043's half of the storefront boundary: the address book
// and the profile answer only for the customer the request PROVES, and refuse
// when nobody was asked.
//
// The tests in api_test.go bind an identity that agrees with the path, because
// what they are about is routing and body handling. Here the identity is a
// party that can DISAGREE, which is the only arrangement in which the check is
// observable at all.

// provenCustomer is the customer the identities in this file prove.
const provenCustomer = "cust_PROVEN"

// claimedByAnother is a customer id a caller might name in the path without
// being able to prove it. It is the whole attack ADR 0008 measured: the
// identifier is not a secret, it travels in every order response, and before
// this gate knowing it was enough to read the person's street address.
const claimedByAnother = "cust_SOMEBODY_ELSE"

// fixedIdentity proves one customer, whatever the request says.
//
// It stands in for the embedder's real verifier — a session cookie, a JWT, a
// header from an upstream proxy. What matters for these tests is only that its
// answer does NOT come from the path, so path and proof can differ.
type fixedIdentity struct {
	customerID string
	err        error
	calls      int
}

var _ corehttp.Identity = (*fixedIdentity)(nil)

func (f *fixedIdentity) CustomerID(*http.Request) (string, error) {
	f.calls++

	return f.customerID, f.err
}

// pathProvingIdentity hands back the customer the path already claimed.
//
// It is the implementation ADR 0043 says gobit cannot detect: it satisfies the
// interface and proves nothing. It exists here because that is exactly what the
// rest of the package's tests need — a bound identity that never refuses — and
// because writing it down is cheaper than a reader rediscovering that the
// framework's guarantee stops at "somebody was asked".
type pathProvingIdentity struct{}

var _ corehttp.Identity = pathProvingIdentity{}

func (pathProvingIdentity) CustomerID(r *http.Request) (string, error) {
	return chi.URLParam(r, "id"), nil
}

// routerWithIdentity mounts the customer routes with the given identity bound.
func routerWithIdentity(svc api.Customer, identity corehttp.Identity) chi.Router {
	r := chi.NewRouter()
	api.New(svc, identity).Routes(r)

	return r
}

// send runs one request against the router and returns the recorded response.
func send(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(
		context.Background(), method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// errorCode reads the machine-readable code out of an error response.
//
// The code is the part a client branches on, so asserting the STATUS alone
// would leave the three refusals here indistinguishable to the one reader who
// has to act on them.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body),
		"the error body could not be read: %s", rec.Body.String())

	return body.Error.Code
}

// customerRoute is one registered storefront route that names a customer.
type customerRoute struct {
	method string
	// pattern is the route as chi registered it, kept for the failure message:
	// a concrete path says which request was sent, the pattern says which
	// registration is at fault.
	pattern string
	path    string
}

// storefrontRoutesNamingACustomer walks the REAL route tree and returns every
// storefront route whose path carries a customer id.
//
// # Why it is walked and not listed
//
// A list of eight paths would pass the day somebody adds a ninth. That is not a
// hypothetical: the address book grew from four routes to six, and a route
// added without the check is indistinguishable from the state this decision was
// written to end — it answers, for anybody who knows an identifier.
//
// Pinning the COUNT would be the other mistake. The number is not the property;
// "every one of them refuses" is, and a test that asserted eight would go red
// for a ninth route that behaves perfectly.
func storefrontRoutesNamingACustomer(t *testing.T, r chi.Router) []customerRoute {
	t.Helper()

	const prefix = "/store/v1/customers/{id}"

	var routes []customerRoute
	err := chi.Walk(r, func(method, route string, _ http.Handler,
		_ ...func(http.Handler) http.Handler,
	) error {
		if !strings.HasPrefix(route, prefix) {
			return nil
		}
		path := strings.NewReplacer(
			"{id}", claimedByAnother,
			"{address_id}", "addr_1",
		).Replace(strings.TrimSuffix(route, "/"))
		routes = append(routes, customerRoute{method: method, pattern: route, path: path})

		return nil
	})
	require.NoError(t, err, "the route tree could not be walked")
	require.NotEmpty(t, routes,
		"no storefront route naming a customer was found, which cannot be true while the "+
			"address book alone registers six. The walk has gone BLIND and every assertion "+
			"made from it is being made about nothing")

	return routes
}

// refusingService is a service every method of which fails the test.
//
// A refusal that still reached the service would be a refusal in name only: the
// row would have been read, and on a write it would have been written. Asserting
// the status alone cannot tell the two apart.
func refusingService(t *testing.T) *stubCustomer {
	t.Helper()

	fail := func(name string) {
		t.Helper()
		t.Errorf("%s was called for a request that had to be refused before it", name)
	}

	return &stubCustomer{
		getCustomerFn: func(context.Context, string) (models.Customer, error) {
			fail("GetCustomer")

			return models.Customer{}, nil
		},
		updateCustomerFn: func(context.Context, string, service.UpdateCustomerInput) (models.Customer, error) {
			fail("UpdateCustomer")

			return models.Customer{}, nil
		},
		listAddressesFn: func(context.Context, string) ([]models.CustomerAddress, error) {
			fail("ListAddresses")

			return nil, nil
		},
		createAddressFn: func(context.Context, string, service.AddressInput) (models.CustomerAddress, error) {
			fail("CreateAddress")

			return models.CustomerAddress{}, nil
		},
		updateAddressFn: func(context.Context, string, string, service.UpdateAddressInput) (models.CustomerAddress, error) {
			fail("UpdateAddress")

			return models.CustomerAddress{}, nil
		},
		deleteAddressFn: func(context.Context, string, string) error {
			fail("DeleteAddress")

			return nil
		},
		setDefaultShipFn: func(context.Context, string, string) (models.CustomerAddress, error) {
			fail("SetDefaultShippingAddress")

			return models.CustomerAddress{}, nil
		},
		setDefaultBillFn: func(context.Context, string, string) (models.CustomerAddress, error) {
			fail("SetDefaultBillingAddress")

			return models.CustomerAddress{}, nil
		},
	}
}

// TestEveryStorefrontRouteNamingACustomerRefusesWhenTheHandlerHoldsNone is the
// closed-by-default half of ADR 0043, held at the HANDLER.
//
// A missing identity does not mean every caller is who they say they are, it
// means nobody looked, so the failure mode of an embedder who never read
// ADR 0008 is a rejected request naming the binding that is missing rather than
// a silent leak of a person's street address.
//
// # What this walk does and does not say
//
// It builds the handler with a literal nil, which is the state [api.New]'s
// godoc calls the SAFE zero value, and it exercises the h.identity == nil
// branch of storeCustomerID on every registered route. That is a real property
// — a handler built by hand, in a test or by an embedder driving this package
// directly, refuses rather than trusts the path.
//
// It is NOT the refusal a running installation makes. Production always passes
// a non-nil wrapper, so the not-bound answer there comes from that wrapper
// finding nothing registered under corehttp.IdentityName. The two branches
// return the same kind and the same code today, and keeping them that way is
// deliberate; but a claim about an INSTALLATION cannot be made from here. It is
// made where the installation is built, over the wired module and an empty
// container: see internal/modules/customer,
// TestEveryStorefrontRouteNamingACustomerRefusesWhenTheContainerHoldsNothing.
func TestEveryStorefrontRouteNamingACustomerRefusesWhenTheHandlerHoldsNone(t *testing.T) {
	t.Parallel()

	svc := refusingService(t)
	r := routerWithIdentity(svc, nil)

	for _, route := range storefrontRoutesNamingACustomer(t, r) {
		t.Run(route.method+" "+route.pattern, func(t *testing.T) {
			rec := send(t, r, route.method, route.path, "")

			require.Equal(t, http.StatusUnauthorized, rec.Code,
				"%s %s answered %d with no identity bound.\n"+
					"ADR 0043: with nothing bound these routes are CLOSED, not open. A route "+
					"that answers here hands a person's name, e-mail and street address to "+
					"anybody who knows an identifier — and the identifier is in every order "+
					"response.\nbody: %s", route.method, route.pattern, rec.Code, rec.Body.String())
			assert.Equal(t, corehttp.CodeIdentityNotBound, errorCode(t, rec),
				"the refusal has to NAME the missing binding; an operator reading a bare 401 "+
					"would look for a credential the client never had to send")
		})
	}
}

// TestTheGuestRegistrationIsNotIdentityChecked pins the one storefront route
// this decision deliberately leaves alone.
//
// POST /store/v1/customers MINTS the customer record. Requiring a proof of the
// customer it is about to create would close the only door through which a
// shopper becomes someone an identity can later prove — and it would do it in
// an installation that had done everything right.
//
// It runs with NO identity bound, which is the strongest form of the claim: the
// route does not merely pass the check, it never reaches it.
func TestTheGuestRegistrationIsNotIdentityChecked(t *testing.T) {
	t.Parallel()

	svc := &stubCustomer{
		registerGuestFn: func(context.Context, service.CustomerInput) (models.Customer, error) {
			return models.Customer{ID: "cust_NEW", Email: "guest@example.com"}, nil
		},
	}

	rec := send(t, routerWithIdentity(svc, nil),
		http.MethodPost, "/store/v1/customers", `{"email":"guest@example.com"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

// TestTheAddressBookRefusesAnIdentityProvingSomebodyElse is the measurement
// ADR 0008 made, run again with the gate in place.
//
// There it produced a 200 and a stranger's address. Here it is a 403 and the
// service is never reached.
func TestTheAddressBookRefusesAnIdentityProvingSomebodyElse(t *testing.T) {
	t.Parallel()

	identity := &fixedIdentity{customerID: provenCustomer}
	r := routerWithIdentity(refusingService(t), identity)

	rec := send(t, r, http.MethodGet,
		"/store/v1/customers/"+claimedByAnother+"/addresses", "")

	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.Equal(t, corehttp.CodeIdentityMismatch, errorCode(t, rec))
	assert.Equal(t, 1, identity.calls, "the identity has to be ASKED, once, per request")
}

// TestTheAddressBookServesTheCustomerTheIdentityProves is the other half of the
// pair, and only the pair says the reason is the proof.
//
// The same route, the same service, the same fake identity: the ONLY difference
// is which customer the path names. A gate that refused everything would pass
// the test above and fail this one, and a reader would never know which of the
// two was broken.
func TestTheAddressBookServesTheCustomerTheIdentityProves(t *testing.T) {
	t.Parallel()

	var asked string
	svc := &stubCustomer{
		listAddressesFn: func(_ context.Context, customerID string) ([]models.CustomerAddress, error) {
			asked = customerID

			return []models.CustomerAddress{{ID: "addr_1", CustomerID: customerID}}, nil
		},
	}
	r := routerWithIdentity(svc, &fixedIdentity{customerID: provenCustomer})

	rec := send(t, r, http.MethodGet, "/store/v1/customers/"+provenCustomer+"/addresses", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, provenCustomer, asked,
		"the service has to be asked for the PROVEN customer; asking for anything else "+
			"would make the comparison decoration")
}

// TestTheIdentityIsAskedBeforeTheBodyIsRead pins the ORDER of the two steps.
//
// It is not a style point. A handler that decoded first would answer a refused
// request with 422 whenever the body was also malformed, so a caller probing
// for someone else's address book could not tell "you may not" from "your JSON
// is wrong" — and neither could the operator reading the access log. The body
// here is deliberately unparseable and the answer must still be the refusal.
func TestTheIdentityIsAskedBeforeTheBodyIsRead(t *testing.T) {
	t.Parallel()

	r := routerWithIdentity(refusingService(t), &fixedIdentity{customerID: provenCustomer})

	rec := send(t, r, http.MethodPut,
		"/store/v1/customers/"+claimedByAnother+"/addresses/addr_1", `{not json`)

	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.Equal(t, corehttp.CodeIdentityMismatch, errorCode(t, rec))
}

// TestTheEmbedderChoosesTheStatusOfItsOwnRefusal proves the error travels
// UNWRAPPED.
//
// The framework cannot know what a failure to prove an identity means in the
// embedder's world: an expired session is a 401 that asks the shopper to sign
// in again, a suspended account is a 403 that does not, and its identity
// provider being down is a 503 that says try later. gobit hands the error to
// its own writer and lets the kind choose, so a wrap here would replace the
// only party who knows with a guess.
func TestTheEmbedderChoosesTheStatusOfItsOwnRefusal(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		err    error
		status int
	}{
		"a session that expired": {
			err:    coreerrors.Unauthorized("session_expired", "the session has expired"),
			status: http.StatusUnauthorized,
		},
		"an account that may not shop": {
			err:    coreerrors.Forbidden("account_suspended", "the account is suspended"),
			status: http.StatusForbidden,
		},
		"an identity provider that is down": {
			err:    coreerrors.Unavailable("idp_down", "the identity provider is unreachable"),
			status: http.StatusServiceUnavailable,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := routerWithIdentity(refusingService(t),
				&fixedIdentity{customerID: provenCustomer, err: testCase.err})

			rec := send(t, r, http.MethodGet,
				"/store/v1/customers/"+provenCustomer, "")

			require.Equal(t, testCase.status, rec.Code, rec.Body.String())
		})
	}
}

// TestAnIdentityThatProvesNothingIsAServerFault pins the answer to the one
// return the contract forbids.
//
// An implementation that returns an empty identifier AND no error has said
// nothing at all, and the caller cannot tell that apart from a proof of the
// empty customer. Treating it as a mismatch would be a 403 that sends the
// embedder looking for a permission rule; treating it as "guest" would open the
// address book of whichever record carries an empty id. It is the
// implementation that is wrong, so it is a 500 that names the fault.
func TestAnIdentityThatProvesNothingIsAServerFault(t *testing.T) {
	t.Parallel()

	r := routerWithIdentity(refusingService(t), &fixedIdentity{customerID: ""})

	rec := send(t, r, http.MethodGet, "/store/v1/customers/"+provenCustomer, "")

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Equal(t, corehttp.CodeIdentityUnproven, errorCode(t, rec))
}

// TestTheAdminAddressBookAsksForNoCustomerIdentity keeps the two surfaces apart.
//
// The admin copy of these twelve endpoints is reached with an operator's token
// and authorized by a SCOPE. An operator opening a customer's address book is
// not that customer and never will be, so a customer identity in front of the
// admin routes would break the support desk in every installation — and it
// would look, from the outside, exactly like the gate working.
func TestTheAdminAddressBookAsksForNoCustomerIdentity(t *testing.T) {
	t.Parallel()

	var asked string
	svc := &stubCustomer{
		listAddressesFn: func(_ context.Context, customerID string) ([]models.CustomerAddress, error) {
			asked = customerID

			return nil, nil
		},
	}
	// No identity is bound at all: the admin path must not consult one.
	r := routerWithIdentity(svc, nil)

	req := httptest.NewRequestWithContext(
		corehttp.WithPrincipal(context.Background(), corehttp.Principal{
			ID: "user_test", Kind: "user", Scopes: []string{corehttp.ScopeAdmin},
		}),
		http.MethodGet, "/admin/v1/customers/"+claimedByAnother+"/addresses", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, claimedByAnother, asked,
		"the operator's path parameter is the customer, and it stays the customer")
}
