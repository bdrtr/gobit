//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestTheStorefrontListsACategoryWithItsSubcategories is ADR 0261 on the
// production wiring: category_tree_id lists a category's products and its
// subcategories', on the REST listing, the REST facets and the GraphQL
// listing alike, while category_id keeps listing what is filed directly.
func TestTheStorefrontListsACategoryWithItsSubcategories(t *testing.T) {
	ctx := t.Context()
	suffix := fmt.Sprint(time.Now().UnixNano())

	category := func(name string, parent *productmodels.Category) productmodels.Category {
		in := productsvc.CreateCategoryInput{Name: "E2E " + name, Handle: "e2e-subtree-" + name + "-" + suffix}
		if parent != nil {
			in.ParentID = &parent.ID
		}
		created, err := productSvc.CreateCategory(ctx, in)
		require.NoError(t, err)
		return created
	}
	apparel := category("apparel", nil)
	shirts := category("shirts", &apparel)
	linen := category("linen", &shirts)

	fit := "fit-" + suffix
	_, err := productSvc.CreateAttribute(ctx, productsvc.AttributeInput{
		Handle: fit, Title: "Fit", Kind: productmodels.AttributeSelect,
		Options: []productsvc.AttributeOptionInput{{Value: "Slim", Rank: 1}},
	})
	require.NoError(t, err)
	product := func(name string, categories ...string) string {
		created, err := productSvc.CreateProduct(ctx, productsvc.CreateProductInput{
			Handle: "e2e-subtree-" + name + "-" + suffix, Title: "E2E Subtree " + name,
			Status: productmodels.StatusPublished, CategoryIDs: categories,
		})
		require.NoError(t, err)
		_, err = productSvc.SetProductAttributes(ctx, created.ID, []productsvc.ProductAttributeInput{
			{Attribute: fit, Options: []string{"slim"}},
		})
		require.NoError(t, err)
		return created.ID
	}
	underLinen := product("linen", linen.ID)
	onApparel := product("apparel", apparel.ID)
	product("elsewhere")

	tree := storefrontCatalog(t, publishableKey, testChannelID, url.Values{"category_tree_id": {apparel.ID}})
	assert.ElementsMatch(t, []string{underLinen, onApparel}, tree.kimlikler(),
		"the tree lists the category's product and its grandchild's")
	assert.Equal(t, 2, tree.Count)

	direct := storefrontCatalog(t, publishableKey, testChannelID, url.Values{"category_id": {apparel.ID}})
	assert.Equal(t, []string{onApparel}, direct.kimlikler(), "category_id lists what is filed directly")

	facets := magazaIstegi(t, catalogPath(testChannelID, "/product-facets")+"?"+url.Values{
		"category_tree_id": {shirts.ID}, "attribute": {fit + ":slim"},
	}.Encode(), publishableKey)
	require.Equal(t, http.StatusOK, facets.Code, facets.Body.String())
	var counted struct {
		Data []productsvc.Facet `json:"data"`
	}
	require.NoError(t, json.Unmarshal(facets.Body.Bytes(), &counted))
	var fitFacet *productsvc.Facet
	for i := range counted.Data {
		if counted.Data[i].Handle == fit {
			fitFacet = &counted.Data[i]
		}
	}
	require.NotNil(t, fitFacet, facets.Body.String())
	assert.Equal(t, int64(1), fitFacet.Products, "the facets count the subtree of shirts, which holds the linen product")

	rec := gqlRequest(t, publishableKey, `query($tree: ID) {
		products(categoryTreeId: $tree, limit: 50) { items { id } count }
	}`, map[string]any{"tree": apparel.ID})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Data struct {
			Products struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
				Count int `json:"count"`
			} `json:"products"`
		} `json:"data"`
		Errors []any `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	require.Empty(t, body.Errors, rec.Body.String())
	listed := make([]string, 0, len(body.Data.Products.Items))
	for _, item := range body.Data.Products.Items {
		listed = append(listed, item.ID)
	}
	assert.ElementsMatch(t, []string{underLinen, onApparel}, listed, "GraphQL lists the same subtree")
	assert.Equal(t, 2, body.Data.Products.Count)
}
