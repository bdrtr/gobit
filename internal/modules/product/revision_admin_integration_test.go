//go:build integration

package product_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestThePanelReadsAndRestoresAProductsHistory is ADR 0316 against a real
// PostgreSQL: the panel's surface lists the revisions newest first with
// what each changed and its total, a restore at a version the product has
// moved past is refused and writes nothing, and one at the version read
// writes the revision back as a new one and names what it left out.
func TestThePanelReadsAndRestoresAProductsHistory(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	surface := service.NewAdminSurface(svc)
	tag, err := svc.CreateTag(ctx, uniqueHandle("history-tag"))
	require.NoError(t, err)
	product, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("history"), Title: "First title", Status: models.StatusDraft, TagIDs: []string{tag.ID},
	})
	require.NoError(t, err)
	_, err = svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{Title: ptrString("Second title")})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteTag(ctx, tag.ID))

	type row struct {
		Version int64    `json:"version"`
		Changed []string `json:"changed"`
	}
	list := func() ([]row, int64) {
		t.Helper()

		raw, total, err := surface.RevisionsJSON(ctx, product.ID, 25, 0)
		require.NoError(t, err)
		var rows []row
		require.NoError(t, json.Unmarshal(raw, &rows))

		return rows, total
	}

	rows, total := list()
	current := storedVersion(t, product.ID)
	require.Equal(t, int64(len(rows)), total)
	require.GreaterOrEqual(t, len(rows), 2)
	assert.Equal(t, current, rows[0].Version, "newest first, the current one on top")
	assert.Equal(t, int64(1), rows[len(rows)-1].Version)
	assert.Empty(t, rows[len(rows)-1].Changed, "the first revision names no change")
	assert.NotNil(t, rows[len(rows)-1].Changed, "an empty list is a list")
	raw, _, err := surface.RevisionsJSON(ctx, product.ID, 1, 1)
	require.NoError(t, err)
	var second []row
	require.NoError(t, json.Unmarshal(raw, &second))
	require.Len(t, second, 1, "a page of one")
	assert.Equal(t, rows[1].Version, second[0].Version, "the second page starts after the first")

	_, err = surface.RestoreRevision(ctx, product.ID, 1, current-1)
	require.Error(t, err)
	assert.True(t, errors.IsPreconditionFailed(err), "a page read before the last write is refused: %v", err)
	assert.Equal(t, current, storedVersion(t, product.ID), "and wrote nothing")

	dropped, err := surface.RestoreRevision(ctx, product.ID, 1, current)
	require.NoError(t, err)
	assert.Equal(t, []string{"tag:" + tag.ID}, dropped)
	restored, err := svc.GetProduct(ctx, product.ID)
	require.NoError(t, err)
	assert.Equal(t, "First title", restored.Title)
	after, _ := list()
	assert.Equal(t, current+1, after[0].Version, "the restore is a revision of its own")

	_, _, err = surface.RevisionsJSON(ctx, "prod_missing", 25, 0)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "an unknown product has no history: %v", err)
}
