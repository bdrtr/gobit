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

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/b2b/api"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
	"github.com/bdrtr/gobit/internal/modules/b2b/service"
)

// This file holds ADR 0057's half of the b2b storefront boundary: where an
// identity IS bound, the two routes answer only for the customer it proves;
// where none is bound, they answer as they always have.
//
// The second half is a test of what the decision DECLINED to do, and it is here
// on purpose. A narrowing that quietly became a refusal for every installation
// that never bound a verifier would be caught nowhere else in this tree — the
// e2e harness binds one, and so does every test next door.
//
// The tests in api_test.go bind an identity that agrees with the path, because
// what they are about is routing and scopes. Here the identity is a party that
// can DISAGREE, which is the only arrangement in which the check is observable.

// provenCustomer is the customer the identities in this file prove.
const provenCustomer = "cust_PROVEN"

// claimedByAnother is a customer id a caller might name in the path without
// being able to prove it. Nothing about it is secret: a customer identifier
// travels in cart and order response bodies, and before this gate knowing one
// was enough to read that person's employer and spending limit.
const claimedByAnother = "cust_SOMEBODY_ELSE"

// fixedIdentity proves one customer, whatever the request says.
//
// It stands in for the embedder's real verifier — a session cookie, a JWT, a
// header from an upstream proxy. What matters is only that its answer does NOT
// come from the path, so path and proof can differ.
type fixedIdentity struct {
	customerID string
}

var _ corehttp.Identity = fixedIdentity{}

func (f fixedIdentity) CustomerID(*http.Request) (string, error) { return f.customerID, nil }

// refusingService is a service whose storefront method fails the test.
//
// A refusal that still reached the service would be a refusal in name only: the
// membership would have been read, and the 404 it returns for a stranger is
// itself an answer — it says whether the identifier belongs to an employee of
// some company. Asserting the status alone cannot tell the two apart.
func refusingService(t *testing.T) *stubB2B {
	t.Helper()

	return &stubB2B{
		membershipFn: func(context.Context, string) (service.Membership, error) {
			t.Error("MembershipOfCustomer was called for a request that had to be refused before it")

			return service.Membership{}, nil
		},
	}
}

// routerWithIdentity mounts the b2b routes with the given identity bound.
//
// A nil identity is the installation that bound NONE, and it is spelled by
// handing the handler a lookup that finds nothing rather than no lookup at all
// — the two are the same to the handler by construction, and this is the one
// the module actually wires.
func routerWithIdentity(svc api.B2B, identity corehttp.Identity) chi.Router {
	return routerWithPolicy(svc, identity, false)
}

// routerWithPolicy is the same router with the installation's answer to "may an
// unverified claim be served" spelled out.
//
// It is a second helper rather than a fourth argument on the first because
// every caller but two is asking about a BOUND identity, where the setting
// changes nothing: [corehttp.ProvenCustomer] compares, and a comparison does
// not consult a policy.
func routerWithPolicy(svc api.B2B, identity corehttp.Identity, trustUnverified bool) chi.Router {
	r := chi.NewRouter()
	api.New(svc, func(context.Context) (corehttp.Identity, error) {
		return identity, nil
	}, trustUnverified).Routes(r)

	return r
}

// storefrontRoutesNamingACustomer walks the REAL route tree and returns every
// storefront route that names a customer.
//
// # Why it is walked and not listed
//
// A list of two paths would pass the day somebody adds a third, and the route
// added without the check is indistinguishable from the state this decision
// ended — it answers, for anybody who knows an identifier. Pinning the COUNT
// would be the other mistake: the number is not the property, "every one of
// them refuses" is.
func storefrontRoutesNamingACustomer(t *testing.T, r chi.Router) []string {
	t.Helper()

	var paths []string
	err := chi.Walk(r, func(_, pattern string, _ http.Handler,
		_ ...func(http.Handler) http.Handler,
	) error {
		if !strings.Contains(pattern, "{customer_id}") {
			return nil
		}
		paths = append(paths, strings.ReplaceAll(pattern, "{customer_id}", claimedByAnother))

		return nil
	})
	require.NoError(t, err, "the route tree could not be walked")
	require.NotEmpty(t, paths,
		"no storefront route naming a customer was found, which cannot be true while this "+
			"module registers the company and the employee record. The walk has gone BLIND "+
			"and every assertion made from it is being made about nothing")

	return paths
}

// send runs one storefront GET against the router and returns the recorded
// response.
//
// It carries no admin principal, which is what a storefront request looks
// like: the store surface's credential is the publishable key and the guard
// that checks it is not in this router.
func send(t *testing.T, r chi.Router, path string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodGet, path, http.NoBody)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// errorCode reads the machine-readable code out of an error response.
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

// TestTheStorefrontStillAnswersWhenNoIdentityIsBound pins what ADR 0057
// deliberately did NOT do.
//
// These two routes ship working. An installation that bound no verifier reads a
// company and a spending limit through them today, and the first draft of this
// decision took that away — an upgrade would have turned every b2b storefront
// read into a 401 for an embedder who had done nothing wrong. The narrowing is
// the MISMATCH, which breaks only a caller who was lying.
//
// The residue is real and is not hidden: with nothing bound, the path is
// believed, so a caller who knows an identifier still reads that person's
// employer. The module logs a WARN naming the slot, ADR 0057 states it in
// Consequences, and binding a verifier is what closes it.
func TestTheStorefrontStillAnswersWhenNoIdentityIsBound(t *testing.T) {
	t.Parallel()

	stub := &stubB2B{
		membershipFn: func(_ context.Context, customerID string) (service.Membership, error) {
			assert.Equal(t, claimedByAnother, customerID,
				"with nothing bound, the customer the PATH names is the one served")

			return service.Membership{
				Company:  models.Company{ID: "comp_01", Name: "Unchecked Co"},
				Employee: models.CompanyEmployee{ID: "compemp_01", CompanyID: "comp_01"},
			}, nil
		},
	}
	r := routerWithPolicy(stub, nil, true)

	for _, path := range storefrontRoutesNamingACustomer(t, r) {
		t.Run(path, func(t *testing.T) {
			rec := send(t, r, path)

			require.Equal(t, http.StatusOK, rec.Code,
				"%s answered %d with no identity bound and the claim TRUSTED.\nADR 0057 "+
					"narrows the claim it can CONTRADICT; ADR 0125 made serving an unverified "+
					"one a choice, and an installation that made it keeps this surface.\n"+
					"body: %s", path, rec.Code, rec.Body.String())
		})
	}
}

// TestTheStorefrontRefusesAnUnverifiedClaimByDEFAULT is the other half, and it
// is the half that ships.
//
// Between ADR 0057 and ADR 0125 there was no choice to make: these two routes
// answered 200 for whatever customer the path named, so a caller holding an
// identifier — which travels in every order response — read that person's
// employer and allowance. The surface is not withdrawn; getting it without
// deciding is.
func TestTheStorefrontRefusesAnUnverifiedClaimByDEFAULT(t *testing.T) {
	t.Parallel()

	stub := &stubB2B{
		membershipFn: func(context.Context, string) (service.Membership, error) {
			assert.Fail(t, "the service must not be reached at all",
				"a refusal that happens after the read has already told the caller "+
					"whether that person exists")

			return service.Membership{}, nil
		},
	}
	r := routerWithPolicy(stub, nil, false)

	for _, path := range storefrontRoutesNamingACustomer(t, r) {
		t.Run(path, func(t *testing.T) {
			rec := send(t, r, path)

			require.Equal(t, http.StatusUnauthorized, rec.Code,
				"%s answered %d with nothing bound and the default policy; the closed "+
					"answer is the one an installation that made no choice gets.\nbody: %s",
				path, rec.Code, rec.Body.String())
			assert.Equal(t, corehttp.CodeIdentityNotBound, errorCode(t, rec))
		})
	}
}

// TestTheStorefrontRefusesAnIdentityProvingSomebodyElse is the measurement
// ADR 0008 made, run again with the gate in place.
//
// It produced a 200, a company and a spending limit. Here it is a 403 and the
// service is never reached.
func TestTheStorefrontRefusesAnIdentityProvingSomebodyElse(t *testing.T) {
	t.Parallel()

	r := routerWithIdentity(refusingService(t), fixedIdentity{customerID: provenCustomer})

	for _, path := range storefrontRoutesNamingACustomer(t, r) {
		t.Run(path, func(t *testing.T) {
			rec := send(t, r, path)

			require.Equal(t, http.StatusForbidden, rec.Code,
				"%s answered %d for a customer the request cannot prove.\nbody: %s",
				path, rec.Code, rec.Body.String())
			assert.Equal(t, corehttp.CodeIdentityMismatch, errorCode(t, rec))
		})
	}
}

// TestTheStorefrontAnswersTheProvenCustomer is the other side of the same gate.
//
// Without it the file would pass on a handler that refused EVERYTHING, which is
// a way of closing a hole this repository has actually shipped.
func TestTheStorefrontAnswersTheProvenCustomer(t *testing.T) {
	t.Parallel()

	stub := &stubB2B{
		membershipFn: func(_ context.Context, customerID string) (service.Membership, error) {
			assert.Equal(t, provenCustomer, customerID,
				"the service must be handed the PROVEN customer")

			return service.Membership{
				Company:  models.Company{ID: "comp_01", Name: "Proven Co"},
				Employee: models.CompanyEmployee{ID: "compemp_01", CompanyID: "comp_01"},
			}, nil
		},
	}
	r := routerWithIdentity(stub, fixedIdentity{customerID: provenCustomer})

	rec := send(t, r, "/store/v1/b2b/customers/"+provenCustomer+"/company")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}
