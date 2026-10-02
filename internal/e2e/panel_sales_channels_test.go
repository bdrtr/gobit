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

// TestAnOperatorCorrectsASalesChannelInThePanel is ADR 0352 on the
// production wiring: the Sales channels screen lists a channel through the
// auth module's channel entity; its row corrects the channel through the
// registered `auth.admin` surface; and the same form sent again, now stale,
// is refused on the screen with the channel as it is now.
func TestAnOperatorCorrectsASalesChannelInThePanel(t *testing.T) {
	ctx := t.Context()
	root := corehttp.WithPrincipal(ctx, corehttp.Principal{ID: "usr_root", Kind: "user", Scopes: []string{corehttp.ScopeAdmin}})
	n := fixtureCounter.Add(1)
	name := fmt.Sprintf("e2e panel channel %d", n)
	channel, err := authSvc.CreateSalesChannel(root, authsvc.SalesChannelInput{Name: name, Description: "first"})
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
	// drawn is the channel's row form as the screen was drawn, if listed.
	drawn := func(body string) string {
		_, row, found := strings.Cut(body, `action="`+adminui.SalesChannelsPath+`/`+channel.ID+`?`)
		if !found {
			return ""
		}
		row, _, _ = strings.Cut(row, "</form>")

		return row
	}

	form := drawn(send(http.MethodGet, adminui.SalesChannelsPath, nil).Body.String())
	require.NotEmpty(t, form, "the newest channel is listed first")
	assert.Contains(t, form, `name="read_name" value="`+name+`"`)
	assert.Contains(t, form, `name="read_description" value="first"`)

	correction := url.Values{
		"read_name": {name}, "read_description": {"first"}, "read_disabled": {"false"},
		"name": {name + " renamed"}, "description": {"second"}, "disabled": {"1"},
	}
	written := send(http.MethodPost, adminui.SalesChannelsPath+"/"+channel.ID, correction)
	require.Equal(t, http.StatusSeeOther, written.Code, written.Body.String())
	stored, err := authSvc.GetSalesChannel(ctx, channel.ID)
	require.NoError(t, err)
	assert.Equal(t, name+" renamed|second|true", fmt.Sprintf("%s|%s|%t", stored.Name, stored.Description, stored.IsDisabled))

	stale := send(http.MethodPost, adminui.SalesChannelsPath+"/"+channel.ID, correction)
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "draw the list again")
}

// TestAnOperatorMakesASalesChannelInThePanel is ADR 0353 on the production
// wiring: the Sales channels screen makes a channel through the registered
// `auth.admin` surface, which the screen then lists first as it was typed,
// and refuses a second with the same name.
func TestAnOperatorMakesASalesChannelInThePanel(t *testing.T) {
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
	name := fmt.Sprintf("e2e made channel %d", fixtureCounter.Add(1))
	form := url.Values{"name": {name}, "description": {"by telephone"}, "disabled": {"1"}}

	made := send(http.MethodPost, adminui.SalesChannelsPath, form)
	require.Equal(t, http.StatusSeeOther, made.Code, made.Body.String())
	page := send(http.MethodGet, made.Header().Get("Location"), nil).Body.String()
	assert.Contains(t, page, "Channel "+name+" was written.")
	_, row, found := strings.Cut(page, "<td>"+name+"<br>")
	require.True(t, found, "the new channel is listed first")
	row, _, _ = strings.Cut(row, "</tr>")
	assert.Contains(t, row, "<td>by telephone</td>")
	assert.Contains(t, row, `<span class="pill">disabled</span>`)

	again := send(http.MethodPost, adminui.SalesChannelsPath, form)
	require.Equal(t, http.StatusUnprocessableEntity, again.Code, again.Body.String())
	assert.Contains(t, again.Body.String(), `name="name" value="`+name+`"`, "the refused form keeps what was typed")
}
