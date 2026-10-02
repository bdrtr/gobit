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

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
)

// TestAnOperatorChangesAUsersPrivilegesInThePanel is ADR 0347 on the
// production wiring: a user's page carries the privileges they hold as read
// through the registered `auth.admin` surface; an operator holding admin
// changes them there, and the same form sent again, now stale, is refused on
// the page with the privileges as they are now.
func TestAnOperatorChangesAUsersPrivilegesInThePanel(t *testing.T) {
	ctx := t.Context()
	admin := corehttp.WithPrincipal(ctx, corehttp.Principal{ID: "usr_root", Kind: "user", Scopes: []string{corehttp.ScopeAdmin}})
	clerk, err := authSvc.CreateUser(admin, authsvc.CreateUserInput{
		Email: fmt.Sprintf("e2e-scopes-%d@example.com", fixtureCounter.Add(1)), Scopes: []string{"order:read"},
	}, mfaTestPassword)
	require.NoError(t, err)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_owner", Kind: "user", Scopes: []string{corehttp.ScopeAdmin},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	page := adminui.UsersPath + "/" + clerk.ID

	drawn := send(http.MethodGet, page, nil)
	require.Equal(t, http.StatusOK, drawn.Code, drawn.Body.String())
	assert.Contains(t, drawn.Body.String(), `name="read_scope" value="order:read"`)
	assert.Contains(t, drawn.Body.String(), `value="order:read" checked> order:read`)

	form := url.Values{"read_scope": {"order:read"}, "scope": {"order:read", "invoice:read"}}
	written := send(http.MethodPost, page+"/scopes", form)
	require.Equal(t, http.StatusSeeOther, written.Code, written.Body.String())
	assert.Contains(t, send(http.MethodGet, written.Header().Get("Location"), nil).Body.String(),
		"The privileges were written.")
	stored, err := authSvc.GetUser(ctx, clerk.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"order:read", "invoice:read"}, stored.Scopes)

	stale := send(http.MethodPost, page+"/scopes", url.Values{"read_scope": {"order:read"}, "scope": {}})
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "draw the page again")
	assert.Contains(t, stale.Body.String(), `name="read_scope" value="invoice:read"`,
		"the page is drawn from the privileges as they are now")
	stored, err = authSvc.GetUser(ctx, clerk.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"order:read", "invoice:read"}, stored.Scopes, "the stale form wrote nothing")
}
