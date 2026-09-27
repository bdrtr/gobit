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
func TestTheCatalogComesBackIn(t *testing.T) {
	ctx := t.Context()
	variantID := newVariant(ctx, t, "E2E Import", map[string]int64{taxedCurrency: 5_000})

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
	row[at("product_title")] = "E2E Import, renamed"
	fresh := make([]string, len(header))
	handle := fmt.Sprintf("e2e-imported-%d", fixtureCounter.Add(1))
	fresh[at("product_handle")], fresh[at("product_title")] = handle, "E2E Imported"

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
}
