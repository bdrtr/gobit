//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAProductsRevisionsAreReadAndRestoredOverTheAdminSurface is ADR 0221 on the
// production wiring: a product created and edited over the admin surface lists
// its revisions with what changed, the edit's revision names the request it was
// made in (the id the admin audit log records its caller under), one revision is read with its snapshot, a restore writes
// the first one back as a new revision, and the storefront does not answer the
// version.
func TestAProductsRevisionsAreReadAndRestoredOverTheAdminSurface(t *testing.T) {
	handle := fmt.Sprintf("e2e-revised-%d", time.Now().UnixNano())

	rec, err := adminRequestWithBody(http.MethodPost, "/admin/v1/products", map[string]any{
		"handle": handle, "title": "Revised shirt", "status": "published",
		"variants": []map[string]any{{"title": "One size"}},
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created struct {
		Data struct {
			ID      string `json:"id"`
			Version int64  `json:"version"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	id := created.Data.ID
	assert.Equal(t, int64(1), created.Data.Version)
	require.NoError(t, bindChannel(id, testChannelID))

	rec, err = adminRequestWithBody(http.MethodPatch, "/admin/v1/products/"+id,
		map[string]any{"title": "Revised shirt, linen", "subtitle": "Summer"})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	editRequest := rec.Header().Get("X-Request-Id")
	require.NotEmpty(t, editRequest)

	rec, err = adminRequestWithBody(http.MethodGet, "/admin/v1/products/"+id+"/revisions", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var listing struct {
		Data []struct {
			Version   int64           `json:"version"`
			Changed   []string        `json:"changed"`
			RequestID string          `json:"request_id"`
			Snapshot  json.RawMessage `json:"snapshot"`
		} `json:"data"`
		Count int `json:"count"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listing))
	require.Equal(t, 2, listing.Count)
	assert.Equal(t, int64(2), listing.Data[0].Version)
	assert.Equal(t, []string{"subtitle", "title"}, listing.Data[0].Changed)
	assert.Equal(t, editRequest, listing.Data[0].RequestID, "the request the edit was made in")
	assert.Nil(t, listing.Data[0].Snapshot)

	rec, err = adminRequestWithBody(http.MethodGet, "/admin/v1/products/"+id+"/revisions/1", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var first struct {
		Data struct {
			Snapshot map[string]any `json:"snapshot"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &first))
	assert.Equal(t, "Revised shirt", first.Data.Snapshot["title"])

	rec, err = adminRequestWithBody(http.MethodPost, "/admin/v1/products/"+id+"/revisions/1/restore", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var restored struct {
		Data struct {
			Product struct {
				Title    string  `json:"title"`
				Subtitle *string `json:"subtitle"`
				Version  int64   `json:"version"`
			} `json:"product"`
			Dropped []string `json:"dropped"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &restored))
	assert.Equal(t, "Revised shirt", restored.Data.Product.Title)
	assert.Nil(t, restored.Data.Product.Subtitle, "the subtitle the first revision did not have is cleared")
	assert.Equal(t, int64(3), restored.Data.Product.Version)
	assert.Equal(t, []string{}, restored.Data.Dropped)

	rec, err = adminRequestWithBody(http.MethodPost, "/admin/v1/products/"+id+"/revisions/9/restore", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())

	store := magazaIstegi(t, catalogPath(testChannelID, "/products/"+handle), publishableKey)
	require.Equal(t, http.StatusOK, store.Code, store.Body.String())
	assert.NotContains(t, store.Body.String(), `"version"`, "the version is the admin surface's")
}
