//go:build integration

package e2e

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/jobs/productimport"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// importCSV posts a file to the import endpoint through the production guards.
func importCSV(t *testing.T, file []byte) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/admin/v1/products/imports", bytes.NewReader(file))
	request.Header.Set("Authorization", "Bearer "+secretKey)
	request.Header.Set("Content-Type", "text/csv")
	recorder := httptest.NewRecorder()
	testRouter.ServeHTTP(recorder, request)

	return recorder
}

// TestTheCatalogComesBackIn is ADR 0205 on the production wiring: a row of the
// export, edited, and a new product, sent to the endpoint, applied by the job
// the installation registers, and read back through the import's status.
//
// The prices are ADR 0207's: the edited row's price is written at one unit
// through the pricing module the installation holds, its quantity tier keeps
// its amount, and the new product's variant is given a price set.
func TestTheCatalogComesBackIn(t *testing.T) {
	ctx := t.Context()
	variantID := newVariant(ctx, t, "E2E Import", map[string]int64{taxedCurrency: 5_000})
	links, err := productSvc.VariantLinkIDs(ctx, variantID)
	require.NoError(t, err)
	require.NotNil(t, links.PriceSetID)
	nine := int32(9)
	_, err = pricingSvc.SetPrices(ctx, *links.PriceSetID, []pricingsvc.PriceInput{
		{CurrencyCode: taxedCurrency, Amount: 5_000, MinQuantity: 1, MaxQuantity: &nine},
		{CurrencyCode: taxedCurrency, Amount: 4_000, MinQuantity: 10},
	})
	require.NoError(t, err)
	priceColumn := "variant_price_" + strings.ToLower(taxedCurrency)

	exported := adminCartRequest(t, http.MethodGet, "/admin/v1/products/export", "")
	require.Equal(t, http.StatusOK, exported.Code, exported.Body.String())
	records, err := csv.NewReader(strings.NewReader(exported.Body.String())).ReadAll()
	require.NoError(t, err)
	header := records[0]
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
	productID := row[at("product_id")]
	require.Equal(t, "5000", row[at(priceColumn)], "the export wrote the price at one unit")
	row[at("product_title")] = "E2E Import, renamed"
	row[at(priceColumn)] = "6000"
	fresh := make([]string, len(header))
	handle := fmt.Sprintf("e2e-imported-%d", fixtureCounter.Add(1))
	fresh[at("product_handle")], fresh[at("product_title")] = handle, "E2E Imported"
	fresh[at("variant_title")], fresh[at("variant_sku")] = "One size", handle
	fresh[at(priceColumn)] = "7000"

	var file bytes.Buffer
	writer := csv.NewWriter(&file)
	require.NoError(t, writer.WriteAll([][]string{header, row, fresh}))

	accepted := importCSV(t, file.Bytes())
	require.Equal(t, http.StatusAccepted, accepted.Code, accepted.Body.String())
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(accepted.Body.Bytes(), &created))

	// The job as the installation registers it, run until the import ends.
	run := productimport.Definition(productSvc, nil).Run
	var status struct {
		Data struct {
			Status      string `json:"status"`
			RowsDone    int    `json:"rows_done"`
			RowsCreated int    `json:"rows_created"`
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
	assert.Equal(t, 2, status.Data.RowsDone)
	assert.Equal(t, 1, status.Data.RowsUpdated)
	assert.Equal(t, 1, status.Data.RowsCreated)
	assert.Zero(t, status.Data.RowsFailed)

	renamed, err := productSvc.GetProduct(ctx, productID)
	require.NoError(t, err)
	assert.Equal(t, "E2E Import, renamed", renamed.Title)
	imported, err := productSvc.GetProductByHandle(ctx, handle)
	require.NoError(t, err)
	assert.Equal(t, "E2E Imported", imported.Title)

	stored, err := pricingSvc.ListPrices(ctx, *links.PriceSetID)
	require.NoError(t, err)
	amounts := map[int32]int64{}
	for _, price := range stored {
		assert.Equal(t, taxedCurrency, price.CurrencyCode, "an empty cell adds no currency")
		amounts[price.MinQuantity] = price.Amount
	}
	assert.Equal(t, map[int32]int64{1: 6_000, 10: 4_000}, amounts, "the tier keeps its amount")

	require.Len(t, imported.Variants, 1)
	freshLinks, err := productSvc.VariantLinkIDs(ctx, imported.Variants[0].ID)
	require.NoError(t, err)
	require.NotNil(t, freshLinks.PriceSetID, "the new variant was given a price set")
	freshPrices, err := pricingSvc.ListPrices(ctx, *freshLinks.PriceSetID)
	require.NoError(t, err)
	require.Len(t, freshPrices, 1)
	assert.Equal(t, int64(7_000), freshPrices[0].Amount)
}
