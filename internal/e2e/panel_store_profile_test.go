//go:build integration

package e2e

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
)

// panelProfileReadAt reads the moment the store profile form carries.
var panelProfileReadAt = regexp.MustCompile(`name="read_updated_at" value="([^"]*)"`)

// TestAnOperatorWritesTheStoreProfileInThePanel is ADR 0336 on the
// production wiring: the Store profile form carries the moment the profile
// was last written through the registered `settings.admin` surface, to the
// microsecond the database holds; the profile is written from it, and the
// same form sent again, now stale, is refused with the profile as it is now.
func TestAnOperatorWritesTheStoreProfileInThePanel(t *testing.T) {
	t.Cleanup(func() { writeStoreProfile(t) })
	writeStoreProfile(t)
	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_owner", Kind: "user", Scopes: []string{"settings:read", "settings:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	page := send(http.MethodGet, adminui.StoreProfilePath, nil).Body.String()
	readAt := panelProfileReadAt.FindStringSubmatch(page)
	require.Len(t, readAt, 2, "the form carries the moment the profile was written")
	require.NotEmpty(t, readAt[1], "a profile is written")

	form := url.Values{
		"read_updated_at": {html.UnescapeString(readAt[1])}, "legal_name": {"E2E Panel Shop A.S."},
		"tax_number": {"9876543210"}, "tax_office": {"Besiktas"}, "email": {"panel@example.com"},
		"address": {"1 Panel Way\r\nIstanbul"}, "country_code": {"tr"},
	}
	written := send(http.MethodPost, adminui.StoreProfilePath, form)
	require.Equal(t, http.StatusSeeOther, written.Code, written.Body.String())
	landed := send(http.MethodGet, written.Header().Get("Location"), nil).Body.String()
	assert.Contains(t, landed, "The store profile was written.")
	assert.Contains(t, landed, "<td>E2E Panel Shop A.S.</td>")
	assert.Contains(t, landed, "<td>Besiktas</td>")
	now := panelProfileReadAt.FindStringSubmatch(landed)
	require.Len(t, now, 2)
	assert.NotEqual(t, readAt[1], now[1], "the write moved the moment")

	stale := send(http.MethodPost, adminui.StoreProfilePath, form)
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "draw the page again")
	assert.Contains(t, stale.Body.String(), `name="read_updated_at" value="`+now[1]+`"`,
		"the form is drawn again from the profile as it is now")
}
