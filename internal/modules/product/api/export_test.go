package api_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

const exportPath = "/admin/v1/products/export"

// TestTheExportAnswersWithAFile streams the service's CSV with the headers a
// download needs, and hands the status filter on (ADR 0204).
func TestTheExportAnswersWithAFile(t *testing.T) {
	t.Parallel()

	var got service.ExportOptions
	pages := 0
	catalog := &fakeCatalog{exportProducts: func(
		_ context.Context, out io.Writer, opts service.ExportOptions, afterPage func() error,
	) error {
		got = opts
		_, err := io.WriteString(out, "product_id\nprod_1\n")
		require.NoError(t, err)
		pages++

		return afterPage()
	}}

	rec := do(t, newRouter(catalog), http.MethodGet, exportPath+"?status=draft", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "text/csv; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, `attachment; filename="products.csv"`, rec.Header().Get("Content-Disposition"))
	assert.Equal(t, "product_id\nprod_1\n", rec.Body.String())
	require.NotNil(t, got.Status)
	assert.Equal(t, models.StatusDraft, *got.Status)
	assert.Equal(t, 1, pages)
}

// TestAnExportRefusesAnUnknownStatus before it reads anything.
func TestAnExportRefusesAnUnknownStatus(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{exportProducts: func(context.Context, io.Writer, service.ExportOptions, func() error) error {
		t.Fatal("the export ran with a status that does not exist")
		return nil
	}}

	rec := do(t, newRouter(catalog), http.MethodGet, exportPath+"?status=sold", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestAnExportThatFailsBeforeItsFirstRowIsAnError answers with the error, since
// nothing has been sent yet.
func TestAnExportThatFailsBeforeItsFirstRowIsAnError(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{exportProducts: func(context.Context, io.Writer, service.ExportOptions, func() error) error {
		return coreerrors.Unavailable("region_down", "the regions are unreachable")
	}}

	rec := do(t, newRouter(catalog), http.MethodGet, exportPath, "")

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
}

// TestAnExportThatFailsMidwayDropsTheConnection: a file that stopped in the
// middle must not read as a whole catalog, so the handler aborts rather than
// ending the response.
func TestAnExportThatFailsMidwayDropsTheConnection(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{exportProducts: func(_ context.Context, out io.Writer, _ service.ExportOptions, _ func() error) error {
		_, err := io.WriteString(out, "product_id\nprod_1\n")
		require.NoError(t, err)

		return coreerrors.Unavailable("pricing_down", "the prices are unreachable")
	}}

	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		do(t, newRouter(catalog), http.MethodGet, exportPath, "")
	})
}

// TestTheExportTakesPricingsReadToo: the file carries prices, so a caller
// allowed the catalog and not the prices is refused.
func TestTheExportTakesPricingsReadToo(t *testing.T) {
	t.Parallel()

	router, catalog := scopedRouter(t, "product:read")
	rec := scopeRequest(t, router, http.MethodGet, exportPath, "")
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.Zero(t, catalog.callCount)

	router, catalog = scopedRouter(t, "product:read", "pricing:read")
	rec = scopeRequest(t, router, http.MethodGet, exportPath, "")
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, catalog.callCount)
}
