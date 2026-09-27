package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/api"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// postCSV sends a file to the import endpoint with a full identity.
func postCSV(t *testing.T, catalog *fakeCatalog, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()

	return postCSVAs(t, catalog, []string{corehttp.ScopeAdmin}, contentType, body)
}

// postCSVAs sends a file to the import endpoint with the given scopes.
func postCSVAs(t *testing.T, catalog *fakeCatalog, scopes []string, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/admin/v1/products/imports", strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID: "usr_test", Kind: "user", Scopes: scopes,
	}))
	rec := httptest.NewRecorder()
	newRouter(catalog).ServeHTTP(rec, req)

	return rec
}

// TestAnImportTakesTheFileAsItIs hands the body to the service unchanged and
// answers 202 with the import to ask about (ADR 0205).
func TestAnImportTakesTheFileAsItIs(t *testing.T) {
	t.Parallel()

	var got []byte
	catalog := &fakeCatalog{createImport: func(_ context.Context, file []byte) (models.Import, error) {
		got = file
		return models.Import{ID: "pimp_1", Status: models.ImportPending, RowsTotal: 1}, nil
	}}
	body := "product_handle,product_title\nshirt,Shirt\n"

	rec := postCSV(t, catalog, "text/csv; charset=utf-8", body)

	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	assert.Equal(t, body, string(got))
	data, ok := decodeBody(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "pimp_1", data["id"])
	assert.Equal(t, "pending", data["status"])
}

// TestAnImportIsACSVFileOfBoundedSize refuses another media type and a file
// past the limit before the service sees it.
func TestAnImportIsACSVFileOfBoundedSize(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{createImport: func(context.Context, []byte) (models.Import, error) {
		t.Fatal("the service was handed a file the endpoint should have refused")
		return models.Import{}, nil
	}}

	rec := postCSV(t, catalog, "application/json", `{"product_handle":"shirt"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	rec = postCSV(t, catalog, "text/csv", strings.Repeat("x", service.MaxImportBytes+1))
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "at most")
}

// TestAnImportIsReadBack by its id.
func TestAnImportIsReadBack(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{getImport: func(_ context.Context, id string) (models.Import, error) {
		return models.Import{
			ID: id, Status: models.ImportCompleted, RowsTotal: 2, RowsDone: 2, RowsFailed: 1,
			Errors: []models.ImportError{{Row: 3, Message: "no product prod_x"}},
		}, nil
	}}

	rec := do(t, newRouter(catalog), http.MethodGet, "/admin/v1/products/imports/pimp_1", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, ok := decodeBody(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "completed", data["status"])
	errs, ok := data["errors"].([]any)
	require.True(t, ok)
	require.Len(t, errs, 1)
}

// TestAnImportWithPricesTakesThePricingWrite: a file with price columns writes
// prices, so the catalog's write alone does not send it (ADR 0207).
func TestAnImportWithPricesTakesThePricingWrite(t *testing.T) {
	t.Parallel()

	sent := 0
	catalog := &fakeCatalog{createImport: func(context.Context, []byte) (models.Import, error) {
		sent++
		return models.Import{ID: "pimp_1", Status: models.ImportPending, RowsTotal: 1}, nil
	}}
	priced := "product_handle,variant_sku,variant_price_try\nshirt,S1,1000\n"
	plain := "product_handle,product_title\nshirt,Shirt\n"

	rec := postCSVAs(t, catalog, []string{api.ScopeWrite}, "text/csv", priced)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "pricing:write")
	assert.Zero(t, sent, "the file was not kept")

	rec = postCSVAs(t, catalog, []string{api.ScopeWrite}, "text/csv", plain)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

	rec = postCSVAs(t, catalog, []string{api.ScopeWrite, "pricing:write"}, "text/csv", priced)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	assert.Equal(t, 2, sent)
}
