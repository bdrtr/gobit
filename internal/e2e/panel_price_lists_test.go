//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
)

// priceListsTotal reads the count the price lists' screen prints.
var priceListsTotal = regexp.MustCompile(`(\d+) price lists\.`)

// TestAnOperatorWritesAPriceListInThePanel is ADR 0326 on the production
// wiring: the Price lists form writes an override list with its window
// through the pricing module's registered surface, the list's last page
// names it with its type, status and window, and a window that ends before
// it starts is refused by the module in the panel's language.
func TestAnOperatorWritesAPriceListInThePanel(t *testing.T) {
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

	title := fmt.Sprintf("E2E Wholesale %d", fixtureCounter.Add(1))
	written := send(http.MethodPost, adminui.PriceListsPath, url.Values{
		"title": {title}, "type": {"override"}, "status": {"active"},
		"starts_at": {"2027-01-01T00:00"}, "ends_at": {"2027-12-31T23:59"},
	})
	require.Equal(t, http.StatusSeeOther, written.Code, written.Body.String())
	landed := send(http.MethodGet, written.Header().Get("Location"), nil).Body.String()
	assert.Contains(t, landed, "Price list "+title+" was written.")

	count := priceListsTotal.FindStringSubmatch(landed)
	require.Len(t, count, 2, "the list prints its count")
	total, err := strconv.Atoi(count[1])
	require.NoError(t, err)
	last := send(http.MethodGet, adminui.PriceListsPath+"?page="+strconv.Itoa((total+24)/25), nil).Body.String()
	_, row, found := strings.Cut(last, title)
	require.True(t, found, "the list is on the last page")
	row, _, _ = strings.Cut(row, "</tr>")
	for _, want := range []string{"<td>override</td>", `<span class="pill">active</span>`, "2027-01-01 00:00", "2027-12-31 23:59"} {
		assert.Contains(t, row, want)
	}

	backwards := send(http.MethodPost, adminui.PriceListsPath, url.Values{
		"title": {title + " backwards"}, "type": {"sale"}, "status": {"draft"},
		"starts_at": {"2027-12-31T00:00"}, "ends_at": {"2027-01-01T00:00"},
	})
	require.Equal(t, http.StatusUnprocessableEntity, backwards.Code, backwards.Body.String())
	assert.Contains(t, backwards.Body.String(), "has to be before its end", "the module's reason, in the panel's language")
}
