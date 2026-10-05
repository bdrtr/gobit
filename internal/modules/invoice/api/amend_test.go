package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// amendingBody is a one-row document of the given kind and reason amending the
// sale's row, as the admin route takes it; it names no buyer and no act.
func amendingBody(kind, reason, saleID, lineID string) string {
	return fmt.Sprintf(`{
		"series_prefix": "GBT", "kind": %q, "currency_code": "TRY",
		"seller": {"name": "Gobit Shop", "country_code": "TR"},
		"amends_invoice_id": %q, "amendment_reason": %q,
		"lines": [{"description": "1 x Red T-Shirt", "quantity": 1, "unit_price": 1000,
			"subtotal": 1000, "tax_rate_bps": 2000, "tax_total": 200, "total": 1200,
			"amends_line_id": %q}],
		"subtotal": 1000, "tax_total": 200, "total": 1200
	}`, kind, saleID, reason, lineID)
}

// TestTheAdminRouteIssuesNoAmendingSale: a price raised after a sale is the
// invoicing flow's, for an act the order recorded (ADR 0406), so the route
// refuses a sale naming a sale; a refund naming its sale and row is filed and
// listed under it.
func TestTheAdminRouteIssuesNoAmendingSale(t *testing.T) {
	t.Parallel()

	r, _ := newTestRouter(t)
	saleID := issue(t, r, "GBT")
	read := do(t, r, http.MethodGet, "/admin/v1/invoices/"+saleID, "")
	require.Equal(t, http.StatusOK, read.Code)
	lines, ok := decodeItem(t, read)["lines"].([]any)
	require.True(t, ok)
	line, ok := lines[0].(map[string]any)
	require.True(t, ok)
	lineID, _ := line["id"].(string)

	rec := do(t, r, http.MethodPost, "/admin/v1/invoices", amendingBody("sale", "price_raised", saleID, lineID))
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "invoice_invalid_input", errorCode(t, rec))

	rec = do(t, r, http.MethodPost, "/admin/v1/invoices", amendingBody("refund", "returned", saleID, lineID))
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	refund := decodeItem(t, rec)
	assert.Equal(t, saleID, refund["amends_invoice_id"])
	assert.Equal(t, "returned", refund["amendment_reason"])
	assert.NotContains(t, refund, "amendment_key", "the route documents no act")

	listed := do(t, r, http.MethodGet, "/admin/v1/invoices?amends="+saleID, "")
	require.Equal(t, http.StatusOK, listed.Code)
	page := decodeList(t, listed)
	require.Len(t, page.Data, 1, "the sale's amendments, and not the sale")
	assert.Equal(t, refund["id"], page.Data[0]["id"])
}
