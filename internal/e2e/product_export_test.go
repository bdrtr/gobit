//go:build integration

package e2e

import (
	"encoding/csv"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheCatalogLeavesAsCSV is ADR 0204 on the production wiring: the export
// is served through the admin guards, reads the prices through the read layer
// from the pricing module, and a price column for each region's currency.
func TestTheCatalogLeavesAsCSV(t *testing.T) {
	ctx := t.Context()
	variantID := newVariant(ctx, t, "=E2E Export", map[string]int64{taxedCurrency: 12_345, untaxedCurrency: 999})

	rec := adminCartRequest(t, http.MethodGet, "/admin/v1/products/export", "")

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "text/csv; charset=utf-8", rec.Header().Get("Content-Type"))
	records, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	require.NoError(t, err)
	require.NotEmpty(t, records)
	header := records[0]
	column := func(name string) int {
		for i, candidate := range header {
			if candidate == name {
				return i
			}
		}
		t.Fatalf("the export has no %q column; header: %v", name, header)

		return -1
	}

	var row []string
	for _, record := range records[1:] {
		if record[column("variant_id")] == variantID {
			row = record
		}
	}
	require.NotNil(t, row, "the variant is not in the export")
	assert.Equal(t, "12345", row[column("variant_price_try")], "the price the pricing module holds")
	assert.Equal(t, "999", row[column("variant_price_eur")])
	assert.Equal(t, "'=E2E Export", row[column("product_title")], "a formula leaves as text")
}
