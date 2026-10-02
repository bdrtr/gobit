//go:build integration

package e2e

import (
	"encoding/json"
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
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
)

// TestAnOperatorInvitesAUserInThePanel is ADR 0348 on the production
// wiring: the Users screen opens a user through the registered `auth.admin`
// surface with the privileges ticked, none when none is, and the
// notification module carries the invitation; an e-mail another user has is
// refused on the screen; and the user's page sends the invitation again.
func TestAnOperatorInvitesAUserInThePanel(t *testing.T) {
	ctx := t.Context()
	root := corehttp.WithPrincipal(ctx, corehttp.Principal{ID: "usr_root", Kind: "user", Scopes: []string{corehttp.ScopeAdmin}})
	n := fixtureCounter.Add(1)
	owner, err := authSvc.CreateUser(root, authsvc.CreateUserInput{
		Email: fmt.Sprintf("e2e-inviter-%d@example.com", n), Scopes: []string{corehttp.ScopeAdmin},
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
			ID: owner.ID, Kind: "user", Scopes: []string{corehttp.ScopeAdmin},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	deliveries, err := container.Resolve[adminui.NotificationLister](ctr, adminui.ServiceNotificationAdmin)
	require.NoError(t, err)
	// newest is the newest delivery's template and how many there are.
	newest := func() (string, int64) {
		t.Helper()

		raw, total, err := deliveries.DeliveriesJSON(ctx, "", "", 1, 0)
		require.NoError(t, err)
		var rows []struct {
			Template string `json:"template"`
		}
		require.NoError(t, json.Unmarshal(raw, &rows))
		if len(rows) == 0 {
			return "", total
		}

		return rows[0].Template, total
	}
	// invite sends the form and returns the user it opened, the notification
	// module having carried one invitation.
	invite := func(form url.Values) string {
		t.Helper()

		_, before := newest()
		rec := send(http.MethodPost, adminui.UsersPath+"/invitations", form)
		require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
		location := rec.Header().Get("Location")
		require.True(t, strings.HasSuffix(location, "?invited=sent"), location)
		id := strings.TrimSuffix(strings.TrimPrefix(location, adminui.UsersPath+"/"), "?invited=sent")
		assert.Contains(t, send(http.MethodGet, location, nil).Body.String(), "An invitation was sent to "+form.Get("email")+".")
		template, after := newest()
		assert.Equal(t, "auth.user_invited|"+fmt.Sprint(before+1), template+"|"+fmt.Sprint(after),
			"the notification module carried one invitation")

		return id
	}

	clerkEmail := fmt.Sprintf("e2e-invited-%d@example.com", n)
	clerk, err := authSvc.GetUser(ctx, invite(url.Values{
		"email": {clerkEmail}, "first_name": {"Grace"}, "last_name": {"Hopper"}, "scope": {"order:read"},
	}))
	require.NoError(t, err)
	assert.Equal(t, clerkEmail+"|Grace|Hopper", clerk.Email+"|"+clerk.FirstName+"|"+clerk.LastName)
	assert.Equal(t, []string{"order:read"}, clerk.Scopes)
	bare, err := authSvc.GetUser(ctx, invite(url.Values{"email": {fmt.Sprintf("e2e-invited-%d-bare@example.com", n)}}))
	require.NoError(t, err)
	assert.Empty(t, bare.Scopes, "no privilege ticked opens no administrator")

	taken := send(http.MethodPost, adminui.UsersPath+"/invitations", url.Values{"email": {clerkEmail}})
	require.Equal(t, http.StatusUnprocessableEntity, taken.Code, taken.Body.String())
	assert.Contains(t, taken.Body.String(), `name="email" value="`+clerkEmail+`"`)

	again := send(http.MethodPost, adminui.UsersPath+"/"+clerk.ID+"/invitation", nil)
	require.Equal(t, http.StatusSeeOther, again.Code, again.Body.String())
	assert.Equal(t, adminui.UsersPath+"/"+clerk.ID+"?invited=sent", again.Header().Get("Location"))
}
