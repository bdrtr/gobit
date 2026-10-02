package identitysession

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// TestARequestWithoutASessionIsRefusedNotFailed is D220 with the identity
// gobit ships: a storefront route naming a customer, asked by a caller with no
// session or a forged one, answers 401 identity_refused. It answered 500,
// because this identity refuses with an error it does not classify and the
// core took that for an internal fault (ADR 0371).
func TestARequestWithoutASessionIsRefusedNotFailed(t *testing.T) {
	t.Parallel()

	sessions := newSessions(t, fixedClock(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)))
	signedIn := httptest.NewRecorder()
	sessions.Issue(signedIn, testCustomerID)

	for name, cookie := range map[string]string{
		"no session":    "",
		"a forged one":  DefaultCookieName + "=" + testCustomerID + ".1790000000.forged",
		"a garbled one": DefaultCookieName + "=not-a-session",
	} {
		request := httptest.NewRequest(http.MethodGet, "/store/v1/customers/"+testCustomerID+"/orders", http.NoBody)
		if cookie != "" {
			request.Header.Set("Cookie", cookie)
		}

		_, err := corehttp.ProvenCustomer(sessions, request, testCustomerID)
		require.Error(t, err, name)
		assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnauthorized), "%s: %v", name, err)
		assert.Equal(t, corehttp.CodeIdentityRefused, coreerrors.CodeOf(err), name)

		proven, err := corehttp.ProvenCustomerIfAny(sessions, request)
		require.NoError(t, err, "%s proves nobody, which a route open to anonymous callers serves", name)
		assert.Empty(t, proven, name)
	}

	request := httptest.NewRequest(http.MethodGet, "/store/v1/customers/"+testCustomerID+"/orders", http.NoBody)
	for _, issued := range signedIn.Result().Cookies() {
		request.AddCookie(issued)
	}
	proven, err := corehttp.ProvenCustomer(sessions, request, testCustomerID)
	require.NoError(t, err, "the session it issued proves its customer")
	assert.Equal(t, testCustomerID, proven)
}
