//go:build integration

package e2e

import (
	"fmt"
	"html"
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
	customersvc "github.com/bdrtr/gobit/internal/modules/customer/service"
)

// TestAnOperatorRevisesACustomerGroupInThePanel is ADR 0329 on the production
// wiring: a group's row on the Customer groups list carries its name and rank
// as drawn, the row's form renames and re-ranks it through the registered
// `customer.admin` surface from those, and the same form sent again, now
// stale, is refused on the list with the group as it is now.
func TestAnOperatorRevisesACustomerGroupInThePanel(t *testing.T) {
	ctx := t.Context()
	n := fixtureCounter.Add(1)
	group, err := customerSvc.CreateGroup(ctx, customersvc.GroupInput{Name: fmt.Sprintf("E2E Panel Silver %d", n), Rank: 1})
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
			ID: "usr_support", Kind: "user", Scopes: []string{"customer:read", "customer:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	// drawn is the form the group's row carries: its action and the name and
	// rank it was drawn with.
	drawn := func(list string) (string, url.Values) {
		t.Helper()

		marker := `action="` + adminui.CustomerGroupListPath + "/" + group.ID
		_, form, found := strings.Cut(list, marker)
		require.True(t, found, "the group's row offers its form")
		query, form, _ := strings.Cut(form, `"`)
		form, _, _ = strings.Cut(form, "</form>")
		read := url.Values{}
		for _, field := range []string{"read_name", "read_rank"} {
			_, value, ok := strings.Cut(form, `name="`+field+`" value="`)
			require.True(t, ok, field)
			value, _, _ = strings.Cut(value, `"`)
			read.Set(field, html.UnescapeString(value))
		}

		return adminui.CustomerGroupListPath + "/" + group.ID + html.UnescapeString(query), read
	}

	action, read := drawn(send(http.MethodGet, adminui.CustomerGroupListPath, nil).Body.String())
	assert.Equal(t, url.Values{"read_name": {group.Name}, "read_rank": {"1"}}, read)
	renamed := fmt.Sprintf("E2E Panel Platinum %d", n)
	form := url.Values{"read_name": read["read_name"], "read_rank": read["read_rank"], "name": {renamed}, "rank": {"-2"}}
	revised := send(http.MethodPost, action, form)
	require.Equal(t, http.StatusSeeOther, revised.Code, revised.Body.String())
	assert.Contains(t, send(http.MethodGet, revised.Header().Get("Location"), nil).Body.String(),
		"Group "+renamed+" was written.")
	stored, err := customerSvc.GetGroup(ctx, group.ID)
	require.NoError(t, err)
	assert.Equal(t, renamed+"|-2", fmt.Sprintf("%s|%d", stored.Name, stored.Rank), "the module holds the group as revised")

	form.Set("name", fmt.Sprintf("E2E Panel Bronze %d", n))
	stale := send(http.MethodPost, action, form)
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "draw the list again")
	_, now := drawn(stale.Body.String())
	assert.Equal(t, url.Values{"read_name": {renamed}, "read_rank": {"-2"}}, now, "the row is drawn from the group as it is now")
	stored, err = customerSvc.GetGroup(ctx, group.ID)
	require.NoError(t, err)
	assert.Equal(t, renamed, stored.Name, "a stale form writes nothing")
}
