package adminui

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
)

// cameBackSentence opens what the order page says under a parcel that came
// back undelivered when the order owes units of its lines again (ADR 0423),
// up to the apostrophe the page escapes.
const cameBackSentence = "This order owes units of this parcel"

// cameBackPage draws order_1's page with a parcel that came back undelivered
// holding items, held whole or not, the order owing what owed says, for an
// operator holding the scopes given.
func cameBackPage(
	t *testing.T, items []map[string]any, heldWhole bool, owed map[string]int64, scopes ...string,
) string {
	t.Helper()

	catalog := linkedOrderCatalog()
	answer := catalog.answer
	opened := time.Date(2026, 9, 4, 12, 20, 0, 0, time.UTC)
	catalog.answer = func(spec query.GraphSpec) ([]query.Record, error, bool) {
		records, err, handled := answer(spec)
		if len(spec.Expand) == 1 && spec.Expand[0].Link == linkOrderFulfillment && len(records) == 1 {
			parcels, _ := records[0][linkOrderFulfillment].([]query.Record)
			records[0][linkOrderFulfillment] = append(slices.Clone(parcels), query.Record{
				"id": "ful_back", "status": "returned", "tracking_number": "TK-BACK",
				"created_at": opened, "shipped_at": ptrTo(opened.Add(time.Hour)),
				"returned_at": ptrTo(opened.Add(72 * time.Hour)), "items": items, "held_whole": heldWhole,
			})
		}

		return records, err, handled
	}
	panel := newCatalogPanel(t, catalog)
	panel.afterSales = &fakeParcelOpener{owed: owed}
	panel.parcels = &fakeParcelMover{}
	panel.scopes = builtInScopes()

	rec := campaignsRequest(panel, http.MethodGet, OrdersPath+"/order_1", nil, scopes...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	require.Contains(t, body, "TK-BACK", "the parcel that came back is on the page")
	for _, spec := range catalog.specs {
		for _, expansion := range spec.Expand {
			if expansion.Link == linkOrderFulfillment {
				assert.Contains(t, expansion.Fields, FieldParcelHeldWhole,
					"the parcels are read with the mark the real read layer answers only when asked")
			}
		}
	}

	return body
}

// TestAParcelThatCameBackSaysWhatTheOrderOwesAgain is ADR 0423 on the order
// page: under a parcel that came back undelivered, and only there, the page
// says the order owes units of its lines again when the open form offers them,
// without naming a cause, since the owed units may be never-shipped ones; and
// it says nothing when the order owes none of them, when the parcel holds no
// order line, as a replacement's does, when it is held whole since before ADR
// 0423, or when the operator cannot open a parcel, so it never asks for what
// would be refused.
func TestAParcelThatCameBackSaysWhatTheOrderOwesAgain(t *testing.T) {
	t.Parallel()

	ring := []map[string]any{{"line_item_id": "oli_ring", "quantity": int64(1)}}
	writer := []string{scopeOrderRead, scopeOrderWrite, scopeFulfillmentRead}

	body := cameBackPage(t, ring, false, map[string]int64{"oli_ring": 1}, writer...)
	assert.Contains(t, body, `<span class="pill">returned</span><br><span class="muted">`+cameBackSentence,
		"the sentence stands under the parcel that came back")
	assert.Equal(t, 1, strings.Count(body, cameBackSentence),
		"a pending or a delivered parcel holds its units, so the sentence is under no other parcel")

	for name, page := range map[string]string{
		"nothing owed on its lines": cameBackPage(t, ring, false, map[string]int64{"oli_other": 2}, writer...),
		"a replacement's parcel":    cameBackPage(t, nil, false, map[string]int64{"oli_ring": 1}, writer...),
		"a parcel held whole":       cameBackPage(t, ring, true, map[string]int64{"oli_ring": 1}, writer...),
		"a reader": cameBackPage(t, ring, false, map[string]int64{"oli_ring": 1},
			scopeOrderRead, scopeFulfillmentRead),
	} {
		assert.NotContains(t, page, cameBackSentence, name)
	}
}
