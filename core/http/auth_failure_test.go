package http_test

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// failingAuthenticator answers every credential with the error it holds.
type failingAuthenticator struct{ err error }

func (f failingAuthenticator) AuthenticateAdmin(context.Context, string, string) (corehttp.Principal, error) {
	return corehttp.Principal{}, f.err
}

func (f failingAuthenticator) AuthenticateStore(context.Context, string) (corehttp.Principal, error) {
	return corehttp.Principal{}, f.err
}

// TestAnAuthenticatorThatCannotAnswerIsNotARefusal is ADR 0364: both guards
// answer a refusal 401 and a failure to reach a verdict with the failure's own
// class, 503 or 500, under the core's sentence rather than the
// authenticator's; an unclassified error stays a refusal.
func TestAnAuthenticatorThatCannotAnswerIsNotARefusal(t *testing.T) {
	t.Parallel()

	secret := "pool exhausted at 10.0.0.7"
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"refused", coreerrors.Unauthorized("auth_invalid_credentials", "no such key"), http.StatusUnauthorized},
		{"out of reach", coreerrors.Unavailable("auth_canceled", "%s", secret), http.StatusServiceUnavailable},
		{"broken", coreerrors.Internal("auth_query_failed", "%s", secret), http.StatusInternalServerError},
		{"wrapped", fmt.Errorf("looking the key up: %w",
			coreerrors.Internal("auth_query_failed", "%s", secret)), http.StatusInternalServerError},
		{"unclassified", stderrors.New(secret), http.StatusUnauthorized},
	}

	for _, c := range cases {
		auth := failingAuthenticator{err: c.err}
		guards := map[string]struct {
			guard  func(http.Handler) http.Handler
			header string
			value  string
		}{
			"admin": {corehttp.RequireAdmin(auth), "Authorization", "Bearer sk_anything"},
			"store": {corehttp.RequireStore(auth, ""), corehttp.PublishableKeyHeader, "pk_anything"},
		}
		for surface, g := range guards {
			reached := false
			handler := g.guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			req.Header.Set(g.header, g.value)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, c.status, rec.Code, "%s, %s", surface, c.name)
			assert.False(t, reached, "%s, %s: the request went on", surface, c.name)
			assert.NotContains(t, rec.Body.String(), secret, "%s, %s: the authenticator's reason leaked", surface, c.name)
			if c.status == http.StatusUnauthorized {
				assert.NotEmpty(t, rec.Header().Get("WWW-Authenticate"), "%s, %s", surface, c.name)
			} else {
				assert.Contains(t, rec.Body.String(), corehttp.CodeAuthenticationUnchecked, "%s, %s", surface, c.name)
				assert.Empty(t, rec.Header().Get("WWW-Authenticate"),
					"%s, %s: a failure asks for no other credential", surface, c.name)
			}
		}
	}

	_, failed := corehttp.AuthenticatorFailure(nil)
	require.False(t, failed, "no error is no failure")
}
