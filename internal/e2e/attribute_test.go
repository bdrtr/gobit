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

// TestAStorefrontFiltersAndCountsByAttributes is ADR 0219 on the production
// wiring: the operator defines a select and a number attribute over the admin
// surface and gives two products values; the storefront reads the vocabulary,
// filters the listing by an option and by a range, counts the facets with the
// fabric filtered and still sees the other fabric, finds the same product over
// GraphQL, and is refused an option the catalog does not have.
func TestAStorefrontFiltersAndCountsByAttributes(t *testing.T) {
	ctx := t.Context()
	suffix := fmt.Sprint(time.Now().UnixNano())
	fabric, chest := "fabric-"+suffix, "chest-"+suffix

	for _, body := range []map[string]any{
		{"handle": fabric, "title": "Fabric", "kind": "select",
			"options": []map[string]any{{"value": "Cotton"}, {"value": "Wool"}}},
		{"handle": chest, "title": "Chest", "kind": "number"},
	} {
		rec, err := adminRequestWithBody(http.MethodPost, "/admin/v1/product-attributes", body)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	}

	collection, err := productSvc.CreateCollection(ctx, productsvc.CreateCollectionInput{
		Title: "E2E Attributes", Handle: "e2e-attributes-" + suffix,
	})
	require.NoError(t, err)
	product := func(name string, values []map[string]any) productmodels.Product {
		p, err := productSvc.CreateProduct(ctx, productsvc.CreateProductInput{
			Handle: name + "-" + suffix, Title: name, Status: productmodels.StatusPublished,
			CollectionID: &collection.ID, Variants: []productsvc.CreateVariantInput{{Title: "One size"}},
		})
		require.NoError(t, err)
		rec, err := adminRequestWithBody(http.MethodPut, "/admin/v1/products/"+p.ID+"/attributes",
			map[string]any{"values": values})
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return p
	}
	shirt := product("shirt", []map[string]any{
		{"attribute": fabric, "options": []string{"cotton"}}, {"attribute": chest, "number": 100},
	})
	sweater := product("sweater", []map[string]any{
		{"attribute": fabric, "options": []string{"wool"}}, {"attribute": chest, "number": 120},
	})

	vocabulary := magazaIstegi(t, "/store/v1/product-attributes", publishableKey)
	require.Equal(t, http.StatusOK, vocabulary.Code, vocabulary.Body.String())
	assert.Contains(t, vocabulary.Body.String(), `"handle":"`+fabric+`"`)

	listed := func(filters ...string) []string {
		envelope := storefrontCatalog(t, publishableKey, testChannelID, url.Values{
			"collection_id": {collection.ID}, "attribute": filters,
		})
		var ids []string
		for _, item := range envelope.Data {
			ids = append(ids, item.ID)
		}
		return ids
	}
	assert.Equal(t, []string{sweater.ID}, listed(fabric+":wool"))
	assert.Equal(t, []string{shirt.ID}, listed(chest+":..110"))
	assert.Empty(t, listed(fabric+":wool", chest+":..110"), "two attributes are ANDed")

	facets := magazaIstegi(t, catalogPath(testChannelID, "/product-facets")+"?"+url.Values{
		"collection_id": {collection.ID}, "attribute": {fabric + ":cotton"},
	}.Encode(), publishableKey)
	require.Equal(t, http.StatusOK, facets.Code, facets.Body.String())
	var counted struct {
		Data []productsvc.Facet `json:"data"`
	}
	require.NoError(t, json.Unmarshal(facets.Body.Bytes(), &counted))
	for _, f := range counted.Data {
		switch f.Handle {
		case fabric:
			require.Len(t, f.Options, 2)
			assert.Equal(t, []int64{1, 1}, []int64{f.Options[0].Products, f.Options[1].Products},
				"wool is still counted with cotton chosen")
		case chest:
			assert.Equal(t, int64(1), f.Products, "within the cotton filter the chest counts the shirt")
			require.NotNil(t, f.Min)
			assert.Equal(t, 100.0, *f.Min)
		}
	}

	graph := gqlRequest(t, publishableKey, fmt.Sprintf(`{ products(collectionId: %q, attributes: [{attribute: %q, min: 110}])
		{ items { id attributes { handle number options { handle } } } } }`, collection.ID, chest), nil)
	require.Equal(t, http.StatusOK, graph.Code, graph.Body.String())
	assert.Contains(t, graph.Body.String(), sweater.ID)
	assert.NotContains(t, graph.Body.String(), shirt.ID)
	assert.Contains(t, graph.Body.String(), `"handle":"wool"`)

	refused := magazaIstegi(t, catalogPath(testChannelID, "/products")+"?"+url.Values{
		"attribute": {fabric + ":silk"},
	}.Encode(), publishableKey)
	assert.Equal(t, http.StatusUnprocessableEntity, refused.Code, refused.Body.String())
}
