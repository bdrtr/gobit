//go:build integration

package e2e

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// TestAnOperatorRevisesAPriceListInThePanel is ADR 0330 on the production
// wiring: a list's row carries its terms as drawn, its start to the
// microsecond the database holds; the row's form renames the list through
// the registered `pricing.admin` surface, the start typed as it was shown
// staying the moment it was; and the same form sent again, now stale, is
// refused on the list with the list as it is now.
func TestAnOperatorRevisesAPriceListInThePanel(t *testing.T) {
	ctx := t.Context()
	n := fixtureCounter.Add(1)
	starts := time.Date(2027, 2, 1, 9, 30, 15, 123456000, time.UTC)
	created, err := pricingSvc.CreatePriceList(ctx, pricingsvc.PriceListInput{
		Title: fmt.Sprintf("E2E Revised %d", n), Description: "before", Type: models.PriceListSale, StartsAt: &starts,
	})
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
			ID: "usr_pricing", Kind: "user", Scopes: []string{"pricing:read", "pricing:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	marker := `action="` + adminui.PriceListsPath + "/" + created.ID + "?page="
	// drawn is the form the list's row carries: its action and the fields
	// it was drawn with.
	drawn := func(page string) (string, url.Values) {
		t.Helper()

		_, form, found := strings.Cut(page, marker)
		require.True(t, found, "the list's row offers its form")
		query, form, _ := strings.Cut(form, `"`)
		form, _, _ = strings.Cut(form, "</form>")
		values := url.Values{}
		for _, field := range []string{"read_title", "read_description", "read_starts_at", "read_ends_at", "starts_at", "ends_at"} {
			_, value, ok := strings.Cut(form, `name="`+field+`" value="`)
			require.True(t, ok, field)
			value, _, _ = strings.Cut(value, `"`)
			values.Set(field, html.UnescapeString(value))
		}

		return adminui.PriceListsPath + "/" + created.ID + "?page=" + html.UnescapeString(query), values
	}

	// The lists come oldest first; the list is on whichever page holds it.
	var page string
	for number := 1; number <= 100 && !strings.Contains(page, marker); number++ {
		page = send(http.MethodGet, adminui.PriceListsPath+"?page="+strconv.Itoa(number), nil).Body.String()
	}
	action, form := drawn(page)
	assert.Equal(t, "2027-02-01T09:30:15.123456Z", form.Get("read_starts_at"), "the start to the microsecond")
	assert.Equal(t, "2027-02-01T09:30", form.Get("starts_at"), "the picker shows the minute")

	renamed := fmt.Sprintf("E2E Renamed %d", n)
	form.Set("title", renamed)
	form.Set("description", "after")
	revised := send(http.MethodPost, action, form)
	require.Equal(t, http.StatusSeeOther, revised.Code, revised.Body.String())
	assert.Contains(t, send(http.MethodGet, revised.Header().Get("Location"), nil).Body.String(),
		"Price list "+renamed+" was written.")
	list, err := pricingSvc.GetPriceList(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, renamed+"|after", list.Title+"|"+list.Description)
	require.NotNil(t, list.StartsAt)
	assert.True(t, starts.Equal(*list.StartsAt), "the start typed as shown stays the moment it was: %s", list.StartsAt)

	stale := send(http.MethodPost, action, form)
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "draw the list again")
	_, now := drawn(stale.Body.String())
	assert.Equal(t, renamed, now.Get("read_title"), "the row is drawn from the list as it is now")
}
