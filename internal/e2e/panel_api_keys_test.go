//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
)

// TestAnOperatorRevokesAnAPIKeyInThePanel is ADR 0350 on the production
// wiring: the API keys screen lists a key through the registered
// `auth.admin` surface with its token only as redacted, and revokes it in
// the operator's name, after which it is listed among the revoked and is
// accepted no more.
func TestAnOperatorRevokesAnAPIKeyInThePanel(t *testing.T) {
	ctx := t.Context()
	root := corehttp.WithPrincipal(ctx, corehttp.Principal{ID: "usr_root", Kind: "user", Scopes: []string{corehttp.ScopeAdmin}})
	n := fixtureCounter.Add(1)
	owner, err := authSvc.CreateUser(root, authsvc.CreateUserInput{
		Email: fmt.Sprintf("e2e-keys-%d@example.com", n), Scopes: []string{corehttp.ScopeAdmin},
	}, mfaTestPassword)
	require.NoError(t, err)
	title := fmt.Sprintf("e2e panel key %d", n)
	key, plaintext, err := authSvc.CreateAPIKey(root, authsvc.CreateAPIKeyInput{
		Type: models.APIKeySecret, Title: title, Scopes: []string{"order:read"}, CreatedBy: owner.ID,
	})
	require.NoError(t, err)

	authenticator, err := container.Resolve[corehttp.Authenticator](ctr, svcAuthInterop)
	require.NoError(t, err)
	_, err = authenticator.AuthenticateAdmin(ctx, "Bearer", plaintext)
	require.NoError(t, err, "the new key is accepted")

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: owner.ID, Kind: "user", Scopes: []string{corehttp.ScopeAdmin},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	// row is the key's row on the tab, or empty when it is not listed.
	row := func(tab string) string {
		t.Helper()

		rec := send(http.MethodGet, adminui.APIKeysPath+"?"+url.Values{"status": {tab}}.Encode(), nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		_, after, found := strings.Cut(rec.Body.String(), "<td>"+title+"<br>")
		if !found {
			return ""
		}
		after, _, _ = strings.Cut(after, "</tr>")

		return after
	}

	open := row("open")
	require.NotEmpty(t, open, "the new key is among the open ones")
	assert.Contains(t, open, "<code>"+key.Redacted+"</code>")
	assert.NotContains(t, open, plaintext, "the token is shown only as redacted")
	assert.Contains(t, open, "<td>order:read</td>")

	revoked := send(http.MethodPost, adminui.APIKeysPath+"/"+key.ID+"/revoke", url.Values{"status": {"open"}})
	require.Equal(t, http.StatusSeeOther, revoked.Code, revoked.Body.String())
	assert.Empty(t, row("open"), "it is open no more")
	assert.Contains(t, row("revoked"), "by "+owner.ID, "it is listed among the revoked, by whom")
	stored, err := authSvc.GetAPIKey(ctx, key.ID)
	require.NoError(t, err)
	assert.NotNil(t, stored.RevokedAt)
	_, err = authenticator.AuthenticateAdmin(ctx, "Bearer", plaintext)
	assert.Error(t, err, "a revoked key is accepted no more")
}
