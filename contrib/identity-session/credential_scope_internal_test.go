package identitysession

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// The credential route writes a password for the customer its path names, so
// whoever may call it may sign in as that customer. The admin ring proves who
// is calling; these tests hold what it may not prove, which is that the caller
// was granted that power and not merely a neighboring one (ADR 0434, D270).

// credentialWriter is an operator holding the privilege the route demands,
// spelled here rather than read from the module's constant so that a constant
// renamed to a broader privilege is a failure and not a rename.
var credentialWriter = &corehttp.Principal{
	ID: "usr_support", Kind: "user", Scopes: []string{"customer-credential:write"},
}

// TestTheCredentialRouteDemandsItsOwnPrivilege is the outside consumer's
// reproduction and the review's: a key holding product:read set a customer's
// password and signed in as them, and one holding customer:write, the grant of
// every operator who corrects an address, could do the same.
func TestTheCredentialRouteDemandsItsOwnPrivilege(t *testing.T) {
	t.Parallel()

	const chosen = "chosen by the caller"
	body := `{"email":"known@example.test","password":"` + chosen + `"}`

	for _, refused := range []struct {
		name  string
		scope string
	}{
		{"a key holding product:read", "product:read"},
		{"an operator holding customer:write", "customer:write"},
	} {
		t.Run(refused.name+" is refused and changes nothing", func(t *testing.T) {
			t.Parallel()

			store, clock, _, r := anchoredModule(t)
			before := signIn(t, r, "the old password")
			clock.advance(time.Millisecond)

			rec := putCredentialAs(t, r, &corehttp.Principal{
				ID: "key_narrow", Kind: "api_key", Scopes: []string{refused.scope},
			}, testCustomerID, body)

			assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "customer-credential:write",
				"the refusal names the privilege the caller lacks")
			assert.Zero(t, store.puts, "a refused caller wrote a credential")
			require.NoError(t, VerifyPassword(store.hash, "the old password"),
				"the customer's password is the one it was")
			assert.Equal(t, http.StatusUnauthorized, post(t, r, "/store/v1/auth/sign-in",
				`{"email":"known@example.test","password":"`+chosen+`"}`).Code,
				"the caller's password signs nobody in")
			assert.Equal(t, http.StatusOK, get(t, r, sessionPath, before).Code,
				"and the customer's session was not ended")
		})
	}

	for _, allowed := range []struct {
		name      string
		principal *corehttp.Principal
	}{
		{"an operator holding customer-credential:write", credentialWriter},
		{"an operator holding admin", &corehttp.Principal{
			ID: "usr_owner", Kind: "user", Scopes: []string{corehttp.ScopeAdmin},
		}},
	} {
		t.Run(allowed.name+" writes it", func(t *testing.T) {
			t.Parallel()

			store, _, _, r := anchoredModule(t)

			rec := putCredentialAs(t, r, allowed.principal, testCustomerID, body)

			require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
			assert.Equal(t, 1, store.puts)
			assert.Equal(t, testCustomerID, store.customerID, "the credential is the path's customer's")
			assert.Equal(t, http.StatusNoContent, post(t, r, "/store/v1/auth/sign-in",
				`{"email":"known@example.test","password":"`+chosen+`"}`).Code)
		})
	}

	t.Run("a request nobody authenticated is asked who it is", func(t *testing.T) {
		t.Parallel()

		store, _, _, r := anchoredModule(t)

		rec := putCredentialAs(t, r, nil, testCustomerID, body)

		assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
		assert.Zero(t, store.puts)
	})
}

// putCredentialAs sends PUT /admin/v1/customer-credentials/{customer_id}
// carrying the principal gobit's admin ring would have put on the request, or
// none when it is nil.
func putCredentialAs(
	t *testing.T, r chi.Router, principal *corehttp.Principal, customerID, body string,
) *httptest.ResponseRecorder {
	t.Helper()

	ctx := context.Background()
	if principal != nil {
		ctx = corehttp.WithPrincipal(ctx, *principal)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPut,
		"/admin/v1/customer-credentials/"+customerID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}
