package adminui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/query"
)

// getInventoryPage sends a GET as a signed-in operator.
func getInventoryPage(panel *UI, path string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	router.Get(InventoryPath, panel.listInventory)

	request := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	request = request.WithContext(corehttp.WithPrincipal(request.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user"}))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request)

	return rec
}

// TestInventoryListShowsWhatItRead covers the ordinary row.
func TestInventoryListShowsWhatItRead(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityInventoryItem: {{
			"id": "inv_1", "sku": "TSHIRT-RED-M", "title": "Red T-Shirt (M)",
			"requires_shipping": true, "available_quantity": int64(42),
		}},
	}}

	rec := getInventoryPage(newCatalogPanel(t, catalog), InventoryPath)

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "TSHIRT-RED-M")
	assert.Contains(t, body, "Red T-Shirt (M)")
	assert.Contains(t, body, "42")
}

// TestAnUnreadableQuantityIsNotShownAsZero is the distinction this screen must
// not lose.
//
// "The total could not be read" and "there are none on the shelf" are different
// facts, and printing 0 for both would send an operator looking for stock that
// is on the shelf — the kind of wrong that looks right.
//
// The unreadable case is a FLOAT and not a missing field, and the difference
// matters to what this test is worth: the inventory provider always produces
// the field when it is asked for, so absence is not reachable. A float is:
// [intValue] refuses one on purpose, because a count that has been through a
// floating-point conversion is a count that may no longer be the one that was
// stored.
func TestAnUnreadableQuantityIsNotShownAsZero(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityInventoryItem: {
			{"id": "inv_1", "sku": "NO-LEVELS", "title": "Unreported",
				"available_quantity": 42.0},
			{"id": "inv_2", "sku": "EMPTY", "title": "Sold out",
				"available_quantity": int64(0)},
		},
	}}

	rec := getInventoryPage(newCatalogPanel(t, catalog), InventoryPath)

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()

	// The rows are compared by SPLITTING the body at the second row's title
	// rather than by matching a substring against the whole page: ">0<" would
	// depend on the template's whitespace, and a test that breaks when a
	// template is reindented is a test nobody trusts.
	unreported, soldOut, found := strings.Cut(body, "Sold out")
	require.True(t, found, "both rows have to be on the page")

	assert.Contains(t, unreported, "unknown", "an unreported total says so")
	assert.NotContains(t, soldOut, "unknown", "a real zero is not 'unknown'")
	assert.Regexp(t, `(?s)Sold out.*?<td class="num">\s*0\s*</td>`, body,
		"a real zero is printed as a zero")
}

// TestTheInventorySectionIsInTheMenu keeps the route and the menu in step.
func TestTheInventorySectionIsInTheMenu(t *testing.T) {
	t.Parallel()

	body := getInventoryPage(newCatalogPanel(t, &fakeCatalog{}), InventoryPath).Body.String()

	assert.Contains(t, body, `href="`+InventoryPath+`" aria-current="page"`)
}
