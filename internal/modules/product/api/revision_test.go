package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/api"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// revisionCatalog answers the revision reads and records what it was asked.
type revisionCatalog struct {
	api.Catalog

	listed   [3]any
	read     [2]any
	restored [2]any
	dropped  []string
}

func (c *revisionCatalog) ListRevisions(
	_ context.Context, productID string, limit, offset int,
) (service.ListResult[models.Revision], error) {
	c.listed = [3]any{productID, limit, offset}
	count := 5
	return service.ListResult[models.Revision]{
		Items: []models.Revision{{
			ID: "prodrev_2", ProductID: productID, Version: 2, RecordedAt: time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC),
			Changed: []string{"title"}, Snapshot: json.RawMessage(`{"title":"x"}`),
		}},
		Count: &count, Limit: limit, Offset: offset,
	}, nil
}

func (c *revisionCatalog) GetRevision(_ context.Context, productID string, version int64) (models.Revision, error) {
	c.read = [2]any{productID, version}
	return models.Revision{ID: "prodrev_2", ProductID: productID, Version: version, Snapshot: json.RawMessage(`{"title":"x"}`)}, nil
}

func (c *revisionCatalog) RestoreRevision(
	_ context.Context, productID string, version int64,
) (service.RestoreResult, error) {
	c.restored = [2]any{productID, version}
	return service.RestoreResult{Product: models.Product{ID: productID, Title: "x", Version: 7}, Dropped: c.dropped}, nil
}

// TestTheRevisionsAreReadAndRestoredOverTheAdminSurface is ADR 0221: the
// listing leaves the snapshot out, one revision carries it, and a restore
// answers the product with its version and what it left out, an empty list
// when it left out nothing.
func TestTheRevisionsAreReadAndRestoredOverTheAdminSurface(t *testing.T) {
	catalog := &revisionCatalog{}
	r := newRouter(catalog)

	rec := do(t, r, http.MethodGet, "/admin/v1/products/prod_1/revisions?limit=10&offset=5", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, [3]any{"prod_1", 10, 5}, catalog.listed)
	body := decodeBody(t, rec)
	items, ok := body["data"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	first, ok := items[0].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, first, "snapshot", "a listing leaves the snapshot out")
	assert.Equal(t, []any{"title"}, first["changed"])

	rec = do(t, r, http.MethodGet, "/admin/v1/products/prod_1/revisions/2", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, [2]any{"prod_1", int64(2)}, catalog.read)
	data, ok := decodeBody(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"title": "x"}, data["snapshot"])

	rec = do(t, r, http.MethodPost, "/admin/v1/products/prod_1/revisions/3/restore", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, [2]any{"prod_1", int64(3)}, catalog.restored)
	data, ok = decodeBody(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{}, data["dropped"], "nothing left out is an empty list, not null")
	product, ok := data["product"].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 7, product["version"], 0, "the admin product carries its version")

	catalog.dropped = []string{"tag:ptag_1"}
	rec = do(t, r, http.MethodPost, "/admin/v1/products/prod_1/revisions/3/restore", "")
	data, ok = decodeBody(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"tag:ptag_1"}, data["dropped"])
}

// TestARevisionIsAddressedByAWholeVersionFromOne: a version that is not a
// whole number from one is refused before the service is asked.
func TestARevisionIsAddressedByAWholeVersionFromOne(t *testing.T) {
	catalog := &revisionCatalog{}
	r := newRouter(catalog)

	for _, version := range []string{"0", "-1", "two", "1.5"} {
		rec := do(t, r, http.MethodGet, "/admin/v1/products/prod_1/revisions/"+version, "")
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, version)
		rec = do(t, r, http.MethodPost, "/admin/v1/products/prod_1/revisions/"+version+"/restore", "")
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, version)
	}
	assert.Nil(t, catalog.read[0])
	assert.Nil(t, catalog.restored[0])
}
