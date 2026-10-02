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

// paymentListPanel is a panel whose catalog answers the collection listing
// with the collections given, each with its order when the order link is
// expanded, over a shop whose lira has two decimals.
func paymentListPanel(t *testing.T, collections ...query.Record) (*UI, *fakeCatalog) {
	t.Helper()

	catalog := &fakeCatalog{
		byEntity: map[string][]query.Record{EntityRegion: {currencyRecord("TRY", 2)}},
		answer: func(spec query.GraphSpec) ([]query.Record, error, bool) {
			if spec.Entity != EntityPaymentCollection {
				return nil, nil, false
			}
			out := make([]query.Record, 0, len(collections))
			for _, collection := range collections {
				record := query.Record{}
				for key, value := range collection {
					if key != linkOrderPayment || len(spec.Expand) > 0 {
						record[key] = value
					}
				}
				out = append(out, record)
			}
			return out, nil, true
		},
	}
	panel := newCatalogPanel(t, catalog)
	panel.scopes = builtInScopes()

	return panel, catalog
}

// lastSpec is the last read the catalog was asked for of the entity.
func lastSpec(t *testing.T, catalog *fakeCatalog, entity string) query.GraphSpec {
	t.Helper()

	for i := len(catalog.specs) - 1; i >= 0; i-- {
		if catalog.specs[i].Entity == entity {
			return catalog.specs[i]
		}
	}
	require.Failf(t, "not read", "the catalog was not asked for %s", entity)

	return query.GraphSpec{}
}

// TestThePaymentsScreenListsTheCollectionsByStatus is ADR 0357: the
// collections authorized are listed when no tab is chosen,
// each with its amount and how much was authorized, captured and refunded
// in its currency's decimals, and when it was opened; a tab lists its own
// status; the order each was taken for is named and linked only for an
// operator who may read the orders, whose read alone expands the link; and
// the screen is in the menu and asks to read the payments.
func TestThePaymentsScreenListsTheCollectionsByStatus(t *testing.T) {
	t.Parallel()

	panel, catalog := paymentListPanel(t, query.Record{
		fieldID: "paycol_1", fieldAmount: int64(240000), fieldCurrencyCod: "TRY",
		fieldAuthorizedAmount: int64(240000), fieldCapturedAmount: int64(100000), fieldRefundedAmount: int64(0),
		fieldCreatedAt:   time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC),
		linkOrderPayment: map[string]any{fieldID: "order_1", fieldDisplayID: int64(1001)},
	})

	rec := campaignsRequest(panel, http.MethodGet, PaymentsPath, nil, scopePaymentRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	spec := lastSpec(t, catalog, EntityPaymentCollection)
	assert.Equal(t, map[string]any{filterPaymentStatus: "authorized"}, spec.Filters, "the ones authorized first")
	assert.Empty(t, spec.Expand, "an operator who may not read the orders reads none")
	assert.Equal(t, []int{paymentsPerPage + 1, 0}, []int{spec.Limit, spec.Offset})
	_, row, _ := strings.Cut(body, "<td>paycol_1</td>")
	row, _, _ = strings.Cut(row, "</tr>")
	for _, want := range []string{
		`<span class="muted">—</span>`, "<td>2400.00 TRY</td>", "<td>1000.00 TRY</td>", "<td>0.00 TRY</td>",
		"<td>2026-10-01 08:00</td>",
	} {
		assert.Contains(t, row, want)
	}
	assert.Equal(t, 2, strings.Count(row, "<td>2400.00 TRY</td>"), "the amount and what was authorized")
	assert.Contains(t, body, `href="`+PaymentsPath+`?status=authorized" aria-current="page">authorized</a>`)
	assert.Contains(t, body, `href="`+PaymentsPath+`"`, "the screen is in the menu")

	rec = campaignsRequest(panel, http.MethodGet, PaymentsPath+"?status=captured", nil, scopePaymentRead, scopeOrderRead)
	spec = lastSpec(t, catalog, EntityPaymentCollection)
	assert.Equal(t, map[string]any{filterPaymentStatus: "captured"}, spec.Filters)
	require.Len(t, spec.Expand, 1, "an operator who may read the orders reads each collection's")
	assert.Equal(t, linkOrderPayment, spec.Expand[0].Link)
	assert.Contains(t, rec.Body.String(), `<a href="`+OrdersPath+`/order_1">#1001</a>`)

	campaignsRequest(panel, http.MethodGet, PaymentsPath+"?status=lost", nil, scopePaymentRead)
	assert.Equal(t, "authorized", lastSpec(t, catalog, EntityPaymentCollection).Filters[filterPaymentStatus])
	rec = campaignsRequest(panel, http.MethodGet, PaymentsPath, nil, scopeOrderRead)
	assert.Equal(t, http.StatusForbidden, rec.Code, "an operator who may not read the payments")

	many := make([]query.Record, 0, paymentsPerPage+1)
	for i := range paymentsPerPage + 1 {
		many = append(many, query.Record{fieldID: fmt.Sprintf("paycol_%02d", i), fieldCurrencyCod: "TRY"})
	}
	panel, catalog = paymentListPanel(t, many...)
	rec = campaignsRequest(panel, http.MethodGet, PaymentsPath+"?status=captured&page=2", nil, scopePaymentRead)
	assert.Equal(t, paymentsPerPage, lastSpec(t, catalog, EntityPaymentCollection).Offset)
	assert.Contains(t, rec.Body.String(), `href="`+PaymentsPath+`?status=captured&amp;page=1">Previous</a>`)
	rec = campaignsRequest(panel, http.MethodGet, PaymentsPath, nil, scopePaymentRead)
	assert.Contains(t, rec.Body.String(), `href="`+PaymentsPath+`?status=authorized&amp;page=2">Next</a>`)
	assert.NotContains(t, rec.Body.String(), fmt.Sprintf("paycol_%02d", paymentsPerPage), "a page holds its size")
}
