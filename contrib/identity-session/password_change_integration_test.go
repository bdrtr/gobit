//go:build integration

package identitysession_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
)

// TestACredentialIsReadByItsCustomer is ADR 0375's read against a real
// PostgreSQL: the address and the hash by the primary key, and no credential
// as ErrNoCredential.
func TestACredentialIsReadByItsCustomer(t *testing.T) {
	m := registered(t)
	store, ok := m.Credentials().(identitysession.CustomerCredentials)
	require.True(t, ok, "the module's own store reads a credential by its customer (ADR 0375)")
	ctx := t.Context()
	customer := "cust_06G8CHANGEREAD000000000"
	require.NoError(t, m.Credentials().Put(ctx, customer, "Change-Read@Example.test", testHash))

	email, hash, err := store.CredentialOf(ctx, customer)
	require.NoError(t, err)
	assert.Equal(t, "change-read@example.test", email, "the folded address the credential signs in with")
	assert.Equal(t, testHash, hash)

	_, _, err = store.CredentialOf(ctx, "cust_06G8NOCREDENTIAL000000")
	require.ErrorIs(t, err, identitysession.ErrNoCredential)
}

// TestChangingAPasswordAgainstTheRealStore: the route is mounted on the
// module's own store; the new password signs in, the old one does not, and a
// session from before the change proves nobody.
func TestChangingAPasswordAgainstTheRealStore(t *testing.T) {
	m := registered(t)
	r := chi.NewRouter()
	m.Routes(r)

	written := sendAsOperator(t, r, http.MethodPut, "/admin/v1/customer-credentials/cust_06G8CHANGEROUTE00000000",
		`{"email":"change@example.test","password":"the old one"}`)
	require.Equal(t, http.StatusNoContent, written.Code, written.Body.String())
	signIn := func(password string) *httptest.ResponseRecorder {
		return send(t, r, http.MethodPost, "/store/v1/auth/sign-in",
			`{"email":"change@example.test","password":"`+password+`"}`)
	}
	elsewhere := signIn("the old one").Result().Cookies()[0]
	here := signIn("the old one").Result().Cookies()[0]

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/store/v1/auth/password",
		strings.NewReader(`{"current_password":"the old one","new_password":"the new one"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", here.Name+"="+here.Value)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	assert.Equal(t, http.StatusUnauthorized, signIn("the old one").Code)
	assert.Equal(t, http.StatusNoContent, signIn("the new one").Code)
	_, err := m.Sessions().CustomerID(requestCarrying(t, elsewhere))
	require.Error(t, err, "the session from before the change proves nobody")
}
