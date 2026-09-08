package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// This file is ADR 0057's half of the contract: the COMPARISON, which moved
// here because three modules needed it and a second copy of an authorization
// rule keeps answering after it drifts.
//
// identity_test.go next door is about the interface being satisfiable and
// bindable from outside. This one is about what the framework does with the
// answer.

// refusingIdentity is an embedder's verifier that cannot prove anybody.
//
// The error is the embedder's own and its KIND is what picks the status, which
// is the property ProvenCustomer must not take away from it.
type refusingIdentity struct{ err error }

func (r refusingIdentity) CustomerID(*http.Request) (string, error) { return "", r.err }

// silentIdentity is a BROKEN implementation: no identifier and no error.
type silentIdentity struct{}

func (silentIdentity) CustomerID(*http.Request) (string, error) { return "", nil }

// provingIdentity proves one customer whatever the request says.
type provingIdentity struct{ id string }

func (p provingIdentity) CustomerID(*http.Request) (string, error) { return p.id, nil }

// TestProvenCustomerAgreesOnlyWithItself is the whole comparison, all five
// branches, in the package that owns them.
//
// The branches are read together on purpose: the reason an implementation that
// proves nothing is an INTERNAL fault rather than a mismatch is only visible
// beside the mismatch it would otherwise be reported as.
func TestProvenCustomerAgreesOnlyWithItself(t *testing.T) {
	t.Parallel()

	embedderRefusal := coreerrors.Unauthorized("session_expired", "sign in again")

	cases := []struct {
		name     string
		identity corehttp.Identity
		claimed  string
		proven   string
		code     string
		status   int
		// unwrapped requires the embedder's own error to arrive as it was
		// written, so the status it chose is the status the client gets.
		unwrapped bool
	}{
		{
			name:     "agreement returns the proven identifier",
			identity: provingIdentity{id: "cus_1"},
			claimed:  "cus_1",
			proven:   "cus_1",
		},
		{
			name:    "nothing bound refuses rather than trusts the claim",
			claimed: "cus_1",
			code:    corehttp.CodeIdentityNotBound,
			status:  http.StatusUnauthorized,
		},
		{
			name:     "a different customer is forbidden",
			identity: provingIdentity{id: "cus_2"},
			claimed:  "cus_1",
			code:     corehttp.CodeIdentityMismatch,
			status:   http.StatusForbidden,
		},
		{
			name:     "an implementation that proves nothing is a server fault",
			identity: silentIdentity{},
			claimed:  "cus_1",
			code:     corehttp.CodeIdentityUnproven,
			status:   http.StatusInternalServerError,
		},
		{
			name:      "the embedder's own error passes through unwrapped",
			identity:  refusingIdentity{err: embedderRefusal},
			claimed:   "cus_1",
			code:      "session_expired",
			status:    http.StatusUnauthorized,
			unwrapped: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/store/v1/carts", http.NoBody)

			proven, err := corehttp.ProvenCustomer(tc.identity, req, tc.claimed)

			if tc.code == "" {
				require.NoError(t, err)
				assert.Equal(t, tc.proven, proven,
					"the PROVEN identifier is what the caller must go on with")

				return
			}

			require.Error(t, err)
			assert.Empty(t, proven, "a refusal may not hand back an identifier to act on")
			assert.Equal(t, tc.code, coreerrors.CodeOf(err),
				"the code is the part a client branches on")
			assert.Equal(t, tc.status, corehttp.StatusFor(err))
			if tc.unwrapped {
				assert.ErrorIs(t, err, embedderRefusal,
					"wrapping the embedder's error would overwrite the status it chose")
			}
		})
	}
}

// TestProvenCustomerDoesNotFoldCase holds the exactness of the comparison.
//
// It is a separate test because it is a separate decision, and one this
// repository has made in the OTHER direction twice: ADR 0038 and ADR 0039 fold
// case where a human types the value. An identifier is not typed, and folding
// it would make two DIFFERENT identifiers match — the caller holding the folded
// twin would act as somebody else.
func TestProvenCustomerDoesNotFoldCase(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/store/v1/carts", http.NoBody)

	_, err := corehttp.ProvenCustomer(provingIdentity{id: "cus_1"}, req, "CUS_1")

	require.Error(t, err, "an identifier is an opaque token, not a name")
	assert.Equal(t, corehttp.CodeIdentityMismatch, coreerrors.CodeOf(err))
}
