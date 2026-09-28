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

	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestTheGraphQLStorefrontReadsTheVocabulary is ADR 0225 on the production
// wiring: the four vocabulary queries answer through the publishable key what
// the REST reads answer, and a category the shop keeps internal is not named.
func TestTheGraphQLStorefrontReadsTheVocabulary(t *testing.T) {
	ctx := t.Context()
	suffix := fmt.Sprint(time.Now().UnixNano())
	parent, err := productSvc.CreateCategory(ctx, productsvc.CreateCategoryInput{Name: "E2E Vocabulary " + suffix})
	require.NoError(t, err)
	shown, err := productSvc.CreateCategory(ctx, productsvc.CreateCategoryInput{
		Name: "E2E Shown " + suffix, ParentID: &parent.ID,
	})
	require.NoError(t, err)
	_, err = productSvc.CreateCategory(ctx, productsvc.CreateCategoryInput{
		Name: "E2E Internal " + suffix, ParentID: &parent.ID, IsInternal: true,
	})
	require.NoError(t, err)
	_, err = productSvc.CreateAttribute(ctx, productsvc.AttributeInput{
		Handle: "vocab-" + suffix, Title: "Vocabulary", Kind: productmodels.AttributeBoolean,
	})
	require.NoError(t, err)

	rec := gqlRequest(t, publishableKey, `query($parent: ID) {
		collections(limit: 100) { items { id } count }
		categories(parentId: $parent) { items { id name } count }
		tags(limit: 100) { items { id } count }
		productAttributes { handle kind }
	}`, map[string]any{"parent": parent.ID})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Data struct {
			Collections struct {
				Items []struct{ ID string } `json:"items"`
				Count int                   `json:"count"`
			} `json:"collections"`
			Categories struct {
				Items []struct{ ID, Name string } `json:"items"`
			} `json:"categories"`
			Tags struct {
				Items []struct{ ID string } `json:"items"`
				Count int                   `json:"count"`
			} `json:"tags"`
			ProductAttributes []struct{ Handle, Kind string } `json:"productAttributes"`
		} `json:"data"`
		Errors []any `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	require.Empty(t, body.Errors, rec.Body.String())

	require.Len(t, body.Data.Categories.Items, 1, "the internal child is not named")
	assert.Equal(t, shown.ID, body.Data.Categories.Items[0].ID)
	assert.Contains(t, body.Data.ProductAttributes, struct{ Handle, Kind string }{"vocab-" + suffix, "boolean"})

	for path, got := range map[string][]string{
		"/store/v1/collections?limit=100": idsOf(body.Data.Collections.Items),
		"/store/v1/tags?limit=100":        idsOf(body.Data.Tags.Items),
	} {
		rest := magazaIstegi(t, path, publishableKey)
		require.Equal(t, http.StatusOK, rest.Code, rest.Body.String())
		var listing struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rest.Body.Bytes(), &listing))
		want := make([]string, 0, len(listing.Data))
		for _, item := range listing.Data {
			want = append(want, item.ID)
		}
		assert.Equal(t, want, got, "%s and the query answer the same page", path)
	}
}

// idsOf lists the ids of a page.
func idsOf(items []struct{ ID string }) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ID)
	}
	return out
}
