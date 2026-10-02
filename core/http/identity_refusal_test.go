package http_test

import (
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// answeringIdentity is an implementation reduced to the answer it gives.
type answeringIdentity struct {
	id  string
	err error
}

func (a answeringIdentity) CustomerID(*http.Request) (string, error) { return a.id, a.err }

// errNoSession is the bare error an implementation like contrib/identity-session
// gives a request with no session.
var errNoSession = stderrors.New("the request carries no valid session")

func storefrontRequest() *http.Request {
	return httptest.NewRequest(http.MethodGet, "/store/v1/customers/cust_1/orders", http.NoBody)
}

// TestAnIdentitysBareErrorIsARefusal is ADR 0371: an error the identity did
// not classify refuses the request as Unauthorized, keeping its cause, where it
// used to reach the client as an internal fault; a classified error is the
// embedder's answer and passes as it came.
func TestAnIdentitysBareErrorIsARefusal(t *testing.T) {
	t.Parallel()

	_, err := corehttp.ProvenCustomer(answeringIdentity{err: errNoSession}, storefrontRequest(), "cust_1")
	require.Error(t, err)
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnauthorized), "a refusal, not a fault: %v", err)
	assert.Equal(t, corehttp.CodeIdentityRefused, coreerrors.CodeOf(err))
	assert.ErrorIs(t, err, errNoSession, "the identity's own reason stays in the chain for the log")

	for name, classified := range map[string]error{
		"its own refusal": coreerrors.Unauthorized("session_expired", "the session ended"),
		"its own outage":  coreerrors.Unavailable("session_store_down", "the session store is out of reach"),
		"its own verdict": coreerrors.Forbidden("account_locked", "the account is locked"),
	} {
		_, err := corehttp.ProvenCustomer(answeringIdentity{err: classified}, storefrontRequest(), "cust_1")
		assert.Same(t, classified, err, "%s passes as the embedder classified it", name)
	}
}

// TestProvenCustomerIfAnyTellsNobodyFromAnOutage is ADR 0371's door for a
// surface open to anonymous callers: a refusal of any kind proves nobody, and
// only a failure to check — or an answer with nothing in it — ends the request.
func TestProvenCustomerIfAnyTellsNobodyFromAnOutage(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		identity corehttp.Identity
		proves   string
		failing  bool
		fails    coreerrors.Kind
	}{
		"nothing bound":          {identity: nil},
		"a proof":                {identity: answeringIdentity{id: "cust_1"}, proves: "cust_1"},
		"a bare refusal":         {identity: answeringIdentity{err: errNoSession}},
		"a classified refusal":   {identity: answeringIdentity{err: coreerrors.Unauthorized("x", "x")}},
		"a classified verdict":   {identity: answeringIdentity{err: coreerrors.Forbidden("x", "x")}},
		"an outage":              {identity: answeringIdentity{err: coreerrors.Unavailable("x", "x")}, failing: true, fails: coreerrors.KindUnavailable},
		"a broken store":         {identity: answeringIdentity{err: coreerrors.Internal("x", "x")}, failing: true, fails: coreerrors.KindInternal},
		"neither proof nor word": {identity: answeringIdentity{}, failing: true, fails: coreerrors.KindInternal},
	} {
		proven, err := corehttp.ProvenCustomerIfAny(tc.identity, storefrontRequest())
		if tc.failing {
			require.Error(t, err, name)
			assert.True(t, coreerrors.HasKind(err, tc.fails), "%s: %v", name, err)
			assert.Empty(t, proven, name)

			continue
		}
		require.NoError(t, err, name)
		assert.Equal(t, tc.proves, proven, name)
	}
}
