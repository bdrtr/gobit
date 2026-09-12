//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	b2bmodels "github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// This file is the embedder half of ADR 0043 and ADR 0057, in the one place
// that can exercise it: a request going through the PRODUCTION guard stack,
// into the real modules, with a customer identity bound the way an installation
// binds one.
//
// # Why the harness binds one at all
//
// Because the check is not observable without one. ADR 0057 narrows a claim a
// bound verifier CONTRADICTS, so an installation that binds nothing has nothing
// to refuse with and this file would be asserting the old behavior. Binding
// one makes this harness the installation that took the decision.
//
// It has a cost the six cart scenarios pay: this verifier refuses a request
// carrying no header, so every body that names a customer has to send the
// proof. That is why openStorefrontCartInCountry sends it — not because gobit
// requires a proof of every installation, but because this one bound a verifier
// that asks for one.

// customerHeader is where this harness's verifier reads its proof.
//
// A header is what an upstream proxy would set, and it is the only shape that
// works for the CART: the claim there is a field in the body, and an identity
// implementation may not consume the body it is being asked about (the contract
// on corehttp.Identity says the request must not be modified). A verifier for
// this surface reads a cookie, a header or the context — never the field it is
// checking.
const customerHeader = "X-E2E-Customer"

// aStranger is a customer identifier a caller can prove while naming somebody
// else. Nothing about an identifier is secret — it travels in cart and order
// response bodies — which is what made believing one a defect.
const aStranger = "cust_SOMEBODY_ELSE"

// storefrontIdentity is the embedder's verifier for this harness.
//
// It PROVES nothing, and saying so is the point rather than an apology: gobit
// cannot tell a real verifier from one that repeats what it was told, and the
// framework's guarantee stops at "somebody was asked" (see corehttp.Identity,
// "What an implementation owes"). What this one buys the suite is the ability
// to send a request that proves a DIFFERENT customer than the body names, which
// is the whole of the check being exercised.
type storefrontIdentity struct{}

var _ corehttp.Identity = storefrontIdentity{}

// CustomerID returns the customer this request proves, or an error.
//
// An absent header is an ERROR and never an empty identifier: the contract
// forbids the empty-and-nil pair, because a caller cannot tell it apart from a
// proof of the empty customer.
func (storefrontIdentity) CustomerID(r *http.Request) (string, error) {
	id := r.Header.Get(customerHeader)
	if id == "" {
		return "", coreerrors.Unauthorized("e2e_no_session",
			"this request carries no customer session")
	}

	return id, nil
}

// identifiedStorefrontRequest makes a storefront request that PROVES the given
// customer.
func identifiedStorefrontRequest(
	t *testing.T, customerID, method, path, body string,
) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(corehttp.PublishableKeyHeader, publishableKey)
	if customerID != "" {
		request.Header.Set(customerHeader, customerID)
	}

	recorder := httptest.NewRecorder()
	testRouter.ServeHTTP(recorder, request)

	return recorder
}

// TestACartCannotBeOpenedForACustomerTheRequestCannotProve is ADR 0057's
// measurement, run again through the production transport.
//
// Before it, this request answered 201 and the body carried the named
// customer's REGISTERED e-mail address — the flow copies it into a cart opened
// without one — so a single request against an identifier that is not a secret
// told a stranger both that the person exists and how to reach them. The
// spending window was the second half: the cart's customer becomes the order's,
// and the order's spend is deducted from that customer's b2b allowance.
//
// The chain is HTTP end to end. A unit test can hold the handler's branch; only
// this one can say the wiring reaches it in the binary that ships.
func TestACartCannotBeOpenedForACustomerTheRequestCannotProve(t *testing.T) {
	ctx := t.Context()

	customerID, _ := newCustomer(ctx, t)

	rejected := identifiedStorefrontRequest(t, aStranger,
		http.MethodPost, "/store/v1/carts",
		fmt.Sprintf(`{"country_code":%q,"customer_id":%q}`, taxedCountry, customerID))

	require.Equal(t, http.StatusForbidden, rejected.Code,
		"a cart opened for a customer the request cannot prove is the open half of "+
			"defect D3; body: %s", rejected.Body.String())
	assert.Equal(t, corehttp.CodeIdentityMismatch, errorCode(t, rejected),
		"the body's code must NAME the reason; a client that cannot tell a refused claim "+
			"from a missing session retries the wrong one. body: %s", rejected.Body.String())
	assert.NotContains(t, rejected.Body.String(), "@",
		"the refusal must not carry an e-mail address: reading one was the leak")
}

// TestAGuestCartIsOpenedWithNoProofAtAll is the other half, and it is what
// makes the decision takeable.
//
// The verifier bound here REFUSES a request that carries no header, and the
// cart opens anyway: a body naming nobody never reaches it. That is the claim
// in its strongest form — the guest path does not merely pass the check, it is
// never asked.
//
// The neighboring claim, that an installation which bound NOTHING keeps
// selling to customers too, cannot be made from this harness: it binds a
// verifier, and a second harness that did not would be a second copy of this
// file's setup. It is held one layer down, over the wired modules and an empty
// container — see the cart and b2b module packages,
// TestAClaimIsServedWhenTheContainerHoldsNoIdentity.
func TestAGuestCartIsOpenedWithNoProofAtAll(t *testing.T) {
	opened := identifiedStorefrontRequest(t, "",
		http.MethodPost, "/store/v1/carts",
		fmt.Sprintf(`{"country_code":%q}`, taxedCountry))

	require.Equal(t, http.StatusCreated, opened.Code,
		"a guest cart must open without any customer identity; body: %s", opened.Body.String())
}

// TestAGuestCartCannotBeHandedToAStranger is the handover, which is the door
// ADR 0043 deliberately left for later.
//
// Knowing a cart's id is the capability to REACH that cart; it was also, until
// ADR 0057, the capability to give it away — to any customer at all, whose
// company allowance would then pay for it.
func TestAGuestCartCannotBeHandedToAStranger(t *testing.T) {
	ctx := t.Context()

	customerID, _ := newCustomer(ctx, t)
	cartID := openStorefrontCartInCountry(t, taxedCountry, "", "")

	rejected := identifiedStorefrontRequest(t, aStranger,
		http.MethodPost, "/store/v1/carts/"+cartID,
		fmt.Sprintf(`{"customer_id":%q}`, customerID))

	require.Equal(t, http.StatusForbidden, rejected.Code,
		"a guest cart may not be handed to a customer the request cannot prove; body: %s",
		rejected.Body.String())
}

// TestTheB2BStorefrontRefusesAStrangersCompany closes the second half of
// defect D3's residue, over HTTP.
//
// These two routes returned the named customer's employer and spending limit to
// anybody holding an identifier. The limit is the number a spending-window
// probe needs, which is why the two halves were closed in one record.
func TestTheB2BStorefrontRefusesAStrangersCompany(t *testing.T) {
	ctx := t.Context()

	customerID, _ := newCustomer(ctx, t)
	limit := int64(500_000)
	b2bCalisan(ctx, t, customerID, &limit, b2bmodels.ResetNever)

	for _, path := range []string{
		"/store/v1/b2b/customers/" + customerID + "/company",
		"/store/v1/b2b/customers/" + customerID + "/employee",
	} {
		rejected := identifiedStorefrontRequest(t, aStranger, http.MethodGet, path, "")

		require.Equal(t, http.StatusForbidden, rejected.Code,
			"%s answered a caller that cannot prove the customer; body: %s",
			path, rejected.Body.String())
		assert.NotContains(t, rejected.Body.String(), "spending_limit",
			"the refusal must not carry the limit it was refusing to show")
	}
}

// TestTheB2BStorefrontAnswersTheProvenCustomer keeps the gate honest.
//
// Without it the file would pass on a surface that refused everything, which is
// a way of "closing" a hole that deletes the feature.
func TestTheB2BStorefrontAnswersTheProvenCustomer(t *testing.T) {
	ctx := t.Context()

	customerID, _ := newCustomer(ctx, t)
	limit := int64(500_000)
	b2bCalisan(ctx, t, customerID, &limit, b2bmodels.ResetNever)

	answered := identifiedStorefrontRequest(t, customerID, http.MethodGet,
		"/store/v1/b2b/customers/"+customerID+"/employee", "")

	require.Equal(t, http.StatusOK, answered.Code, answered.Body.String())
	assert.Contains(t, answered.Body.String(), "spending_limit",
		"the customer's own employee record is exactly what this route is for")
}
