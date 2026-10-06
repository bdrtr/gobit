//go:build integration

package e2e

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/jobs/productimport"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
)

// TestACostTravelsThroughTheCatalogsFile is ADR 0424 on the production wiring:
// the export carries a variant's unit cost in a region's currency, the file
// edited and sent back writes the new cost through the job the installation
// registers, and a cost in a currency no region sells in has no column and is
// left as it was.
func TestACostTravelsThroughTheCatalogsFile(t *testing.T) {
	ctx := t.Context()
	variantID := newVariant(ctx, t, "E2E Costed", map[string]int64{taxedCurrency: 5_000})
	_, err := productSvc.SetVariantCosts(ctx, variantID, []productmodels.VariantCost{
		{CurrencyCode: taxedCurrency, Amount: 2_000}, {CurrencyCode: "XAU", Amount: 1},
	})
	require.NoError(t, err)
	costColumn := "variant_cost_" + strings.ToLower(taxedCurrency)

	exported := adminCartRequest(t, http.MethodGet, "/admin/v1/products/export", "")
	require.Equal(t, http.StatusOK, exported.Code, exported.Body.String())
	records, err := csv.NewReader(strings.NewReader(exported.Body.String())).ReadAll()
	require.NoError(t, err)
	header := records[0]
	assert.NotContains(t, header, "variant_cost_xau", "no region sells in XAU")
	at := func(column string) int {
		for i, name := range header {
			if name == column {
				return i
			}
		}
		t.Fatalf("no column %q", column)
		return -1
	}
	var row []string
	for _, record := range records[1:] {
		if record[at("variant_id")] == variantID {
			row = record
		}
	}
	require.NotNil(t, row)
	require.Equal(t, "2000", row[at(costColumn)], "the export wrote the unit cost")
	row[at(costColumn)] = "2500"

	var file bytes.Buffer
	writer := csv.NewWriter(&file)
	require.NoError(t, writer.WriteAll([][]string{header, row}))
	accepted := importCSV(t, file.Bytes())
	require.Equal(t, http.StatusAccepted, accepted.Code, accepted.Body.String())
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(accepted.Body.Bytes(), &created))

	run := productimport.Definition(productSvc, nil).Run
	var status struct {
		Data struct {
			Status      string `json:"status"`
			RowsUpdated int    `json:"rows_updated"`
			RowsFailed  int    `json:"rows_failed"`
		} `json:"data"`
	}
	deadline := time.Now().Add(time.Minute)
	for status.Data.Status != "completed" && time.Now().Before(deadline) {
		require.NoError(t, run(ctx))
		read := adminCartRequest(t, http.MethodGet, "/admin/v1/products/imports/"+created.Data.ID, "")
		require.Equal(t, http.StatusOK, read.Code, read.Body.String())
		require.NoError(t, json.Unmarshal(read.Body.Bytes(), &status))
	}
	require.Equal(t, "completed", status.Data.Status)
	assert.Zero(t, status.Data.RowsFailed)
	assert.Equal(t, 1, status.Data.RowsUpdated, "the cost is the row's one change")

	costs, err := productSvc.VariantCosts(ctx, variantID)
	require.NoError(t, err)
	byCurrency := map[string]int64{}
	for _, c := range costs {
		byCurrency[c.CurrencyCode] = c.Amount
	}
	assert.Equal(t, map[string]int64{taxedCurrency: 2_500, "XAU": 1}, byCurrency,
		"the file's currency is written and the one it has no column for stays")
}
