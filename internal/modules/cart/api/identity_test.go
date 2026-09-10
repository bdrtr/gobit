package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/cart/api"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// This file holds ADR 0057's half of the cart boundary: a body that NAMES a
// customer has to prove them, and a body that names nobody is untouched.
//
// The two claims are equally load-bearing. The first is what closes the hole;
// the second is what makes the decision takeable at all, because the cart's
// default path is a shopper with no account and a check that also closed the
// guest path would have deleted the feature instead of guarding it.

// theProven is the customer the identities here prove.
const theProven = "cust_PROVEN"

// theClaimed is a customer a caller might name without being able to prove it.
// Nothing about it is secret: a customer identifier travels in cart and order
// response bodies.
const theClaimed = "cust_SOMEBODY_ELSE"

// claimingBodies are the two storefront bodies that carry a customer_id.
//
// It is a list rather than a walk, and that is a real difference from the
// address book's gate next door: there the claim is a PATH segment, so the
// route tree names the population. Here it is a body FIELD, which no router
// knows about. The tree-wide walk over bodies exists — it is in internal/arch,
// TestNoStorefrontSurfaceActsOnACustomerItCannotProve, whose population comes
// from the request types the handlers decode — and this file is the behavioral
// half for the two the cart registers.
var claimingBodies = []struct {
	name   string
	method string
	path   string
	body   func(customerID string) string
	// served is the customer that reached the layer below the handler. It is
	// what the unbound case asserts: a status under 400 says the request was
	// not refused, and only this says the claim was ACTED on.
	served func(*fakeOpening, *fakeCarts) string
}{
	{
		name:   "opening a cart in somebody's name",
		method: http.MethodPost,
		path:   "/store/v1/carts",
		body: func(customerID string) string {
			return fmt.Sprintf(`{"country_code":"TR","customer_id":%q}`, customerID)
		},
		served: func(o *fakeOpening, _ *fakeCarts) string { return o.gotCustomerID },
	},
	{
		name:   "handing a guest cart over",
		method: http.MethodPost,
		path:   "/store/v1/carts/cart_1",
		body: func(customerID string) string {
			return fmt.Sprintf(`{"customer_id":%q}`, customerID)
		},
		served: func(_ *fakeOpening, c *fakeCarts) string { return c.updateInput.CustomerID },
	},
}

// refusingCart is a service and a flow set whose every relevant method fails
// the test.
//
// A refusal that still reached them would be a refusal in name only: the cart
// would have been opened, or handed over, and the flow would have READ the
// named customer's record on the way — which is how the open version answered
// "does this person exist" and "what is their e-mail address".
func refusingCart(t *testing.T) (*fakeCarts, api.Flows) {
	t.Helper()

	opening := &fakeOpening{cartID: "cart_1"}
	svc := &fakeCarts{cart: models.Cart{ID: "cart_1"}}
	t.Cleanup(func() {
		assert.Zero(t, opening.calls,
			"the cart-opening flow was asked to open a cart for a request that had to be "+
				"refused first; the flow READS the named customer's record, so reaching it "+
				"is the leak itself")
		assert.Zero(t, svc.updateCalls,
			"the service was asked to update a cart for a request that had to be refused")
	})

	return svc, api.Flows{Opening: opening, Pricing: &fakePricing{}, Checkout: &fakeCheckout{}}
}

// mountCart builds the router with the given identity bound.
//
// A nil identity is the installation that bound NONE, and it is spelled by
// handing the handler a lookup that finds nothing rather than no lookup at all
// — the two are the same to the handler by construction, and this is the one
// the module actually wires.
func mountCart(svc api.Carts, flows api.Flows, identity corehttp.Identity) http.Handler {
	return mountCartWithPolicy(svc, flows, identity, false)
}

// mountCartWithPolicy is the same handler with the installation's answer to
// "may an unverified claim be served" spelled out.
//
// It is a second helper rather than a fourth argument on the first because
// every caller but two is asking about a BOUND identity or a GUEST body, and
// the setting reaches neither: a comparison does not consult a policy, and an
// empty claim never reaches the comparison.
func mountCartWithPolicy(
	svc api.Carts, flows api.Flows, identity corehttp.Identity, trustUnverified bool,
) http.Handler {
	r := chi.NewRouter()
	api.New(svc, flows, func(context.Context) (corehttp.Identity, error) {
		return identity, nil
	}, trustUnverified).Routes(r)

	return r
}

// claimCode reads the machine-readable code out of an error response.
func claimCode(t *testing.T, rec *httptest.ResponseRecorder) string {
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

// TestABodyNamingACustomerIsStillServedWhenNothingIsBound pins what ADR 0057
// deliberately did NOT do.
//
// The first draft of this decision refused here, and that would have stopped an
// installation which has been selling for a year from opening a cart for any
// customer at all — and with it the b2b spending limit, which only binds a cart
// naming one. The narrowing is the MISMATCH, and a mismatch needs a verifier to
// see it.
//
// So the residue is stated rather than closed: with nothing bound the claim is
// believed, a caller who knows an identifier can open a cart as that customer,
// and the way to close it is to bind one. That sentence is in ADR 0057's
// Consequences and in the WARN the module logs; this test is what keeps it
// TRUE.
func TestABodyNamingACustomerIsStillServedWhenNothingIsBound(t *testing.T) {
	t.Parallel()

	for _, tc := range claimingBodies {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opening := &fakeOpening{cartID: "cart_1"}
			svc := &fakeCarts{
				cart:   models.Cart{ID: "cart_1"},
				detail: models.CartDetail{Cart: models.Cart{ID: "cart_1"}},
			}
			h := mountCartWithPolicy(svc, api.Flows{Opening: opening}, nil, true)

			rec := doRequest(t, h, tc.method, tc.path, tc.body(theClaimed))

			require.Less(t, rec.Code, http.StatusBadRequest,
				"%s %s answered %d with no identity bound and the claim TRUSTED.\n"+
					"ADR 0057 narrows the claim a bound verifier can CONTRADICT; ADR 0125 "+
					"made serving an unverified one a choice, and an installation that made "+
					"it keeps this surface.\nbody: %s",
				tc.method, tc.path, rec.Code, rec.Body.String())
			assert.Equal(t, theClaimed, tc.served(opening, svc),
				"the claim was not refused but was not acted on either, which is the third "+
					"outcome nobody asked for: with nothing bound the customer the BODY "+
					"names is the customer the cart is opened for")
		})
	}
}

// TestABodyNamingACustomerIsRefusedByDEFAULT is the other half, and it is the
// half that ships.
//
// Between ADR 0057 and ADR 0125 there was no choice to make: a body naming a
// customer opened a cart for them in an installation that had bound nothing, so
// a caller holding an identifier — which travels in every order response — shopped
// as that person and spent their B2B allowance. The surface is not withdrawn;
// getting it without deciding is.
func TestABodyNamingACustomerIsRefusedByDEFAULT(t *testing.T) {
	t.Parallel()

	for _, tc := range claimingBodies {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, flows := refusingCart(t)
			h := mountCartWithPolicy(svc, flows, nil, false)

			rec := doRequest(t, h, tc.method, tc.path, tc.body(theClaimed))

			require.Equal(t, http.StatusUnauthorized, rec.Code,
				"%s %s answered %d with nothing bound and the default policy.\nbody: %s",
				tc.method, tc.path, rec.Code, rec.Body.String())
			assert.Equal(t, corehttp.CodeIdentityNotBound, claimCode(t, rec))
		})
	}
}

// TestAGuestBodyIsUntouchedByThePolicy holds the sentence the whole comparison
// was built around.
//
// A body naming nobody is never asked, in an installation that trusts the
// unverified claim and in one that refuses it. A policy that reached the guest
// path would close the door a shopper without an account walks through, which is
// what ADR 0043 refused to do and ADR 0057 found the way around.
func TestAGuestBodyIsUntouchedByThePolicy(t *testing.T) {
	t.Parallel()

	for _, trusted := range []bool{false, true} {
		t.Run(fmt.Sprintf("trust_unverified=%t", trusted), func(t *testing.T) {
			t.Parallel()

			opening := &fakeOpening{cartID: "cart_1"}
			svc := &fakeCarts{
				cart:   models.Cart{ID: "cart_1"},
				detail: models.CartDetail{Cart: models.Cart{ID: "cart_1"}},
			}
			h := mountCartWithPolicy(svc, api.Flows{Opening: opening}, nil, trusted)

			rec := doRequest(t, h, http.MethodPost, "/store/v1/carts", `{"country_code":"TR"}`)

			require.Less(t, rec.Code, http.StatusBadRequest,
				"a guest cart must open whatever the policy says; body: %s", rec.Body.String())
		})
	}
}

// TestABodyNamingSomebodyElseIsForbidden is the measurement ADR 0051 recorded,
// run again with the gate in place.
//
// It opened a cart, deducted a stranger's checkout from the named customer's
// spending window, and handed back that customer's registered e-mail address.
// Here it is a 403 and neither the flow nor the service is reached.
func TestABodyNamingSomebodyElseIsForbidden(t *testing.T) {
	t.Parallel()

	for _, tc := range claimingBodies {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, flows := refusingCart(t)
			h := mountCart(svc, flows, signedInAs(theProven))

			rec := doRequest(t, h, tc.method, tc.path, tc.body(theClaimed))

			require.Equal(t, http.StatusForbidden, rec.Code,
				"%s %s answered %d for a customer the request cannot prove.\nbody: %s",
				tc.method, tc.path, rec.Code, rec.Body.String())
			assert.Equal(t, corehttp.CodeIdentityMismatch, claimCode(t, rec))
		})
	}
}

// TestAGuestBodyIsNeverAskedForAProof is the half that keeps the decision
// takeable.
//
// It runs with NO identity bound, which is the strongest form of the claim: a
// guest cart does not merely pass the check, it never reaches it. An
// installation that has bound nothing sells to guests exactly as it did before
// ADR 0057.
func TestAGuestBodyIsNeverAskedForAProof(t *testing.T) {
	t.Parallel()

	opening := &fakeOpening{cartID: "cart_1"}
	svc := &fakeCarts{detail: models.CartDetail{Cart: models.Cart{ID: "cart_1"}}}
	h := mountCart(svc, api.Flows{Opening: opening}, nil)

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts", `{"country_code":"TR"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, 1, opening.calls, "a guest cart has to reach the opening flow")
	assert.Empty(t, opening.gotCustomerID, "a guest cart carries no customer")
}

// TestAnEmailOnlyHandoverIsNeverAskedForAProof is the same claim on the update
// body, and it is not a duplicate: collecting the shopper's e-mail address at
// the payment step is a guest flow that goes through the endpoint whose OTHER
// field is the handover.
func TestAnEmailOnlyHandoverIsNeverAskedForAProof(t *testing.T) {
	t.Parallel()

	svc := &fakeCarts{cart: models.Cart{ID: "cart_1", Email: "guest@example.com"}}
	h := mountCart(svc, api.Flows{}, nil)

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts/cart_1",
		`{"email":"guest@example.com"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, svc.updateCalls, "an e-mail-only update has to reach the service")
}

// TestTheProvenCustomerIsWhatTheFlowReceives holds the value that travels.
//
// The comparison passing is not the property; what the cart is opened FOR is.
// If the handler passed the claim on rather than the proof, the check above
// would be decoration the day the two are allowed to differ.
func TestTheProvenCustomerIsWhatTheFlowReceives(t *testing.T) {
	t.Parallel()

	opening := &fakeOpening{cartID: "cart_1"}
	svc := &fakeCarts{detail: models.CartDetail{Cart: models.Cart{ID: "cart_1"}}}
	h := mountCart(svc, api.Flows{Opening: opening}, signedInAs(theProven))

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts",
		fmt.Sprintf(`{"country_code":"TR","customer_id":%q}`, theProven))

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, theProven, opening.gotCustomerID)
}

// TestTheEmbeddersOwnErrorPicksTheStatus proves the pass-through at the
// handler, where a client actually meets it.
//
// The core's own test covers the function; this one covers the wiring, and the
// wiring is where a well-meant coreerrors.Wrap would turn an embedder's 503
// into a 500 and send an operator looking for a bug in gobit.
func TestTheEmbeddersOwnErrorPicksTheStatus(t *testing.T) {
	t.Parallel()

	svc, flows := refusingCart(t)
	identity := failingIdentity{err: coreerrors.Unavailable("idp_unreachable", "the IdP is down")}
	h := mountCart(svc, flows, identity)

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts",
		fmt.Sprintf(`{"country_code":"TR","customer_id":%q}`, theClaimed))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	assert.Equal(t, "idp_unreachable", claimCode(t, rec),
		"the embedder's code has to survive; a client that cannot tell a retryable "+
			"outage from a refusal retries the wrong one")
}

// failingIdentity is a bound verifier that cannot answer.
type failingIdentity struct{ err error }

var _ corehttp.Identity = failingIdentity{}

func (f failingIdentity) CustomerID(*http.Request) (string, error) { return "", f.err }
