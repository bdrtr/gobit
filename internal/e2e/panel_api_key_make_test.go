//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
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

// panelKeyMade is the token the key's page shows once.
var panelKeyMade = regexp.MustCompile(`The key (apikey_[0-9A-Z]+) was made\.[^<]*</p>\s*<p><code>([^<]+)</code>`)

// TestAnOperatorMakesAnAPIKeyInThePanel is ADR 0351 on the production
// wiring: the API keys screen makes a secret key through the registered
// `auth.admin` surface, whose token, shown once, is accepted with the
// privileges ticked and with none when none is; and a publishable key is
// attached to the sales channel ticked.
func TestAnOperatorMakesAnAPIKeyInThePanel(t *testing.T) {
	ctx := t.Context()
	root := corehttp.WithPrincipal(ctx, corehttp.Principal{ID: "usr_root", Kind: "user", Scopes: []string{corehttp.ScopeAdmin}})
	n := fixtureCounter.Add(1)
	owner, err := authSvc.CreateUser(root, authsvc.CreateUserInput{
		Email: fmt.Sprintf("e2e-key-maker-%d@example.com", n), Scopes: []string{corehttp.ScopeAdmin},
	}, mfaTestPassword)
	require.NoError(t, err)
	channel, err := authSvc.CreateSalesChannel(root, authsvc.SalesChannelInput{Name: fmt.Sprintf("e2e key channel %d", n)})
	require.NoError(t, err)
	authenticator, err := container.Resolve[corehttp.Authenticator](ctr, svcAuthInterop)
	require.NoError(t, err)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	// makeKey sends the form and returns the key's id and its token.
	makeKey := func(form url.Values) (string, string) {
		t.Helper()

		req := httptest.NewRequest(http.MethodPost, adminui.APIKeysPath, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: owner.ID, Kind: "user", Scopes: []string{corehttp.ScopeAdmin},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		made := panelKeyMade.FindStringSubmatch(rec.Body.String())
		require.Len(t, made, 3, "the page shows the key's token once")

		return made[1], made[2]
	}

	_, token := makeKey(url.Values{"title": {"e2e reports"}, "type": {"secret"}, "scope": {"order:read"}})
	principal, err := authenticator.AuthenticateAdmin(ctx, "Bearer", token)
	require.NoError(t, err, "the token shown is accepted")
	assert.Equal(t, []string{"order:read"}, principal.Scopes)
	_, token = makeKey(url.Values{"title": {"e2e nothing"}, "type": {"secret"}})
	principal, err = authenticator.AuthenticateAdmin(ctx, "Bearer", token)
	require.NoError(t, err)
	assert.Empty(t, principal.Scopes, "no privilege ticked makes no administrator's key")

	id, _ := makeKey(url.Values{"title": {"e2e shop"}, "type": {"publishable"}, "channel": {channel.ID}})
	channels, err := authSvc.SalesChannelsOfAPIKey(ctx, id)
	require.NoError(t, err)
	require.Len(t, channels, 1)
	assert.Equal(t, channel.ID, channels[0].ID, "the publishable key is attached to the channel ticked")
}
