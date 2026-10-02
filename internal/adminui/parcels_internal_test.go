package adminui

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
)

// parcelListPanel is a panel whose catalog answers the parcel listing with the
// parcels given, each with its order when the order link is expanded.
func parcelListPanel(t *testing.T, parcels ...query.Record) (*UI, *fakeCatalog) {
	t.Helper()

	catalog := &fakeCatalog{answer: func(spec query.GraphSpec) ([]query.Record, error, bool) {
		if spec.Entity != EntityFulfillment {
			return nil, nil, false
		}
		out := make([]query.Record, 0, len(parcels))
		for _, parcel := range parcels {
			record := query.Record{}
			for key, value := range parcel {
				if key != linkOrderFulfillment || len(spec.Expand) > 0 {
					record[key] = value
				}
			}
			out = append(out, record)
		}
		return out, nil, true
	}}
	panel := newCatalogPanel(t, catalog)
	panel.scopes = builtInScopes()

	return panel, catalog
}

// TestTheParcelsScreenListsTheParcelsByStatus is ADR 0356: the parcels
// still to be shipped are listed when no tab is chosen, each with its
// carrier, its tracking and when it was opened; a tab lists its own status
// with when each parcel reached it; the order each was opened for is named
// and linked only for an operator who may read the orders, whose read alone
// expands the link; and the screen is in the menu and asks to read the
// fulfillments.
func TestTheParcelsScreenListsTheParcelsByStatus(t *testing.T) {
	t.Parallel()

	opened := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	shipped := time.Date(2026, 10, 2, 9, 30, 0, 0, time.UTC)
	panel, catalog := parcelListPanel(t, query.Record{
		fieldID: "ful_1", fieldParcelProvider: "manual", fieldTrackingNumber: "TRK1",
		fieldTrackingURL: "https://track.example/TRK1", fieldParcelCreated: opened, fieldShippedAt: shipped,
		linkOrderFulfillment: []map[string]any{{fieldID: "order_1", fieldDisplayID: int64(1001)}},
	})

	rec := campaignsRequest(panel, http.MethodGet, ParcelsPath, nil, scopeFulfillmentRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	spec := catalog.specs[len(catalog.specs)-1]
	assert.Equal(t, map[string]any{filterParcelStatus: "pending"}, spec.Filters, "the parcels still to be shipped")
	assert.Empty(t, spec.Expand, "an operator who may not read the orders reads none")
	assert.Equal(t, []int{parcelsPerPage + 1, 0}, []int{spec.Limit, spec.Offset})
	_, row, _ := strings.Cut(body, "<td>ful_1</td>")
	row, _, _ = strings.Cut(row, "</tr>")
	assert.Contains(t, row, `<span class="muted">—</span>`, "no order named")
	assert.Contains(t, row, "<td>manual</td>")
	assert.Contains(t, row, `<a href="https://track.example/TRK1" rel="noopener noreferrer">TRK1</a>`)
	assert.Contains(t, row, "<td>2026-10-01 08:00</td>")
	assert.NotContains(t, row, "2026-10-02", "a pending parcel has no move to show")
	assert.Contains(t, body, `href="`+ParcelsPath+`?status=pending" aria-current="page">pending</a>`)
	assert.Contains(t, body, `href="`+ParcelsPath+`"`, "the screen is in the menu")

	rec = campaignsRequest(panel, http.MethodGet, ParcelsPath+"?status=shipped", nil, scopeFulfillmentRead, scopeOrderRead)
	body = rec.Body.String()
	spec = catalog.specs[len(catalog.specs)-1]
	assert.Equal(t, map[string]any{filterParcelStatus: "shipped"}, spec.Filters)
	require.Len(t, spec.Expand, 1, "an operator who may read the orders reads each parcel's")
	assert.Equal(t, linkOrderFulfillment, spec.Expand[0].Link)
	_, row, _ = strings.Cut(body, "<td>ful_1</td>")
	row, _, _ = strings.Cut(row, "</tr>")
	assert.Contains(t, row, `<a href="`+OrdersPath+`/order_1">#1001</a>`)
	assert.Contains(t, body, "<th>shipped (UTC)</th>")
	assert.Contains(t, row, "<td>2026-10-02 09:30</td>", "when it reached the tab's status")

	campaignsRequest(panel, http.MethodGet, ParcelsPath+"?status=lost", nil, scopeFulfillmentRead)
	assert.Equal(t, "pending", catalog.specs[len(catalog.specs)-1].Filters[filterParcelStatus], "an unknown tab is pending")
	rec = campaignsRequest(panel, http.MethodGet, ParcelsPath, nil, scopeOrderRead)
	assert.Equal(t, http.StatusForbidden, rec.Code, "an operator who may not read the fulfillments")

	many := make([]query.Record, 0, parcelsPerPage+1)
	for i := range parcelsPerPage + 1 {
		many = append(many, query.Record{fieldID: fmt.Sprintf("ful_%02d", i), fieldParcelCreated: opened})
	}
	panel, catalog = parcelListPanel(t, many...)
	rec = campaignsRequest(panel, http.MethodGet, ParcelsPath+"?status=delivered&page=2", nil, scopeFulfillmentRead)
	assert.Equal(t, parcelsPerPage, catalog.specs[len(catalog.specs)-1].Offset)
	assert.Contains(t, rec.Body.String(), `href="`+ParcelsPath+`?status=delivered&amp;page=1">Previous</a>`)
	rec = campaignsRequest(panel, http.MethodGet, ParcelsPath, nil, scopeFulfillmentRead)
	assert.Contains(t, rec.Body.String(), `href="`+ParcelsPath+`?status=pending&amp;page=2">Next</a>`)
	assert.NotContains(t, rec.Body.String(), fmt.Sprintf("ful_%02d", parcelsPerPage), "a page holds its size")
}
