//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
)

// TestAnOperatorListsTheUsersInThePanel is ADR 0345 on the production
// wiring: the Users screen reads the users through the registered
// `auth.admin` surface, each with the privileges they hold and whether they
// have proven an authenticator, found by e-mail, and a user who has proven
// one is listed on that tab and not on the other.
func TestAnOperatorListsTheUsersInThePanel(t *testing.T) {
	ctx := t.Context()
	admin := corehttp.WithPrincipal(ctx, corehttp.Principal{ID: "usr_root", Kind: "user", Scopes: []string{corehttp.ScopeAdmin}})
	n := fixtureCounter.Add(1)
	proven, err := authSvc.CreateUser(admin, authsvc.CreateUserInput{
		Email: fmt.Sprintf("e2e-users-%d-proven@example.com", n), FirstName: "Ada", LastName: "Lovelace",
		Scopes: []string{"order:read", "order:write"},
	}, mfaTestPassword)
	require.NoError(t, err)
	enrollment, err := authSvc.EnrolMFA(ctx, proven.ID, "gobit", "")
	require.NoError(t, err)
	require.NoError(t, authSvc.ConfirmMFA(ctx, proven.ID, totpCodeAt(t, enrollment.Secret, time.Now())))
	missing, err := authSvc.CreateUser(admin, authsvc.CreateUserInput{
		Email: fmt.Sprintf("e2e-users-%d-missing@example.com", n), Scopes: []string{},
	}, mfaTestPassword)
	require.NoError(t, err)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	list := func(query url.Values) string {
		t.Helper()

		req := httptest.NewRequest(http.MethodGet, adminui.UsersPath+"?"+query.Encode(), http.NoBody)
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_owner", Kind: "user", Scopes: []string{"auth:read"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		return rec.Body.String()
	}
	// row is the user's row on the page, or empty when they are not listed.
	row := func(page, email string) string {
		_, after, found := strings.Cut(page, "<td>"+email+"</td>")
		if !found {
			return ""
		}
		after, _, _ = strings.Cut(after, "</tr>")

		return after
	}

	page := list(url.Values{"email": {proven.Email}})
	assert.Contains(t, page, "1 for "+proven.Email+".", "a user is found by their e-mail, and only they")
	found := row(page, proven.Email)
	require.NotEmpty(t, found)
	assert.Contains(t, found, "<td>Ada Lovelace</td>")
	assert.Contains(t, found, "<td>order:read, order:write</td>")
	assert.Contains(t, found, "<td>proven</td>")
	found = row(list(url.Values{"email": {missing.Email}}), missing.Email)
	require.NotEmpty(t, found)
	assert.Contains(t, found, `<span class="muted">none</span>`)
	assert.Contains(t, found, `<span class="pill">missing</span>`)

	page = list(url.Values{"second_factor": {"proven"}})
	assert.NotEmpty(t, row(page, proven.Email), "the newest user with a second factor is on that tab")
	assert.Empty(t, row(page, missing.Email))
	page = list(url.Values{"second_factor": {"missing"}})
	assert.NotEmpty(t, row(page, missing.Email), "and the newest without one on the other")
	assert.Empty(t, row(page, proven.Email))
}
