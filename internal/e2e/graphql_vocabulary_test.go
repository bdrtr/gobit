//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
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
		rest := storeRequest(t, path, publishableKey)
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

// TestTheGraphQLStorefrontCountsWhatRESTCounts is ADR 0226 on the production
// wiring: through the publishable key, productFacets with a collection and an
// attribute answers the facets the REST count answers for the key's channel,
// and optionValues the REST option vocabulary's page. A product sold only in
// another channel and a draft are in neither answer.
func TestTheGraphQLStorefrontCountsWhatRESTCounts(t *testing.T) {
	ctx := t.Context()
	ground := channelCatalogFixture(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	fit, shade := "fit-"+suffix, "E2E Shade "+suffix
	dusk, hidden, draft := "Dusk "+suffix, "Hidden "+suffix, "Draft "+suffix
	_, err := productSvc.CreateAttribute(ctx, productsvc.AttributeInput{
		Handle: fit, Title: "Fit", Kind: productmodels.AttributeSelect,
		Options: []productsvc.AttributeOptionInput{{Value: "Slim", Rank: 1}, {Value: "Loose", Rank: 2}},
	})
	require.NoError(t, err)
	collection, err := productSvc.CreateCollection(ctx, productsvc.CreateCollectionInput{
		Title: "E2E Facets", Handle: "e2e-facets-" + suffix,
	})
	require.NoError(t, err)
	product := func(name, shadeValue, fitOption string, status productmodels.Status) string {
		p, err := productSvc.CreateProduct(ctx, productsvc.CreateProductInput{
			Handle: "e2e-facets-" + name + "-" + suffix, Title: "E2E Facets " + name, Status: status,
			CollectionID: &collection.ID,
			Options:      []productsvc.CreateOptionInput{{Title: shade, Values: []string{shadeValue}}},
			Variants: []productsvc.CreateVariantInput{{
				Title: "One size", Options: map[string]string{shade: shadeValue},
			}},
		})
		require.NoError(t, err)
		_, err = productSvc.SetProductAttributes(ctx, p.ID, []productsvc.ProductAttributeInput{
			{Attribute: fit, Options: []string{fitOption}},
		})
		require.NoError(t, err)
		return p.ID
	}
	product("sold", dusk, "slim", productmodels.StatusPublished)
	require.NoError(t, bindChannel(product("elsewhere", hidden, "loose", productmodels.StatusPublished),
		ground.secondChannelID))
	product("draft", draft, "loose", productmodels.StatusDraft)

	rec := gqlRequest(t, publishableKey, `query($collection: ID, $attributes: [AttributeFilter!]) {
		productFacets(collectionId: $collection, attributes: $attributes) {
			handle title kind options { handle value products } true false products min max
		}
		optionValues(limit: 100) { items { optionTitle value } count }
	}`, map[string]any{
		"collection": collection.ID,
		"attributes": []map[string]any{{"attribute": fit, "options": []string{"slim"}}},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Data struct {
			ProductFacets []productsvc.Facet `json:"productFacets"`
			OptionValues  struct {
				Items []struct{ OptionTitle, Value string } `json:"items"`
				Count int                                   `json:"count"`
			} `json:"optionValues"`
		} `json:"data"`
		Errors []any `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	require.Empty(t, body.Errors, rec.Body.String())

	counted := storeRequest(t, catalogPath(testChannelID, "/product-facets")+"?"+url.Values{
		"collection_id": {collection.ID}, "attribute": {fit + ":slim"},
	}.Encode(), publishableKey)
	require.Equal(t, http.StatusOK, counted.Code, counted.Body.String())
	var rest struct {
		Data []productsvc.Facet `json:"data"`
	}
	require.NoError(t, json.Unmarshal(counted.Body.Bytes(), &rest))
	assert.Equal(t, withoutEmptyOptions(rest.Data), withoutEmptyOptions(body.Data.ProductFacets),
		"the two surfaces count the same catalog")
	assert.Contains(t, body.Data.ProductFacets, productsvc.Facet{
		Handle: fit, Title: "Fit", Kind: productmodels.AttributeSelect, Products: 1,
		Options: []productsvc.FacetOption{
			{Handle: "slim", Value: "Slim", Products: 1}, {Handle: "loose", Value: "Loose", Products: 0},
		},
	}, "the filtered attribute is counted without its own filter")

	vocabulary := storeRequest(t, catalogPath(testChannelID, "/option-values")+"?limit=100", publishableKey)
	require.Equal(t, http.StatusOK, vocabulary.Code, vocabulary.Body.String())
	var page struct {
		Data []struct {
			OptionTitle string `json:"option_title"`
			Value       string `json:"value"`
		} `json:"data"`
		Count int `json:"count"`
	}
	require.NoError(t, json.Unmarshal(vocabulary.Body.Bytes(), &page))
	want := make([]struct{ OptionTitle, Value string }, 0, len(page.Data))
	for _, pair := range page.Data {
		want = append(want, struct{ OptionTitle, Value string }{pair.OptionTitle, pair.Value})
	}
	assert.Equal(t, want, body.Data.OptionValues.Items, "the two surfaces answer the same page")
	assert.Equal(t, page.Count, body.Data.OptionValues.Count)
	assert.Contains(t, body.Data.OptionValues.Items, struct{ OptionTitle, Value string }{shade, dusk})
	for _, value := range []string{hidden, draft} {
		assert.NotContains(t, body.Data.OptionValues.Items, struct{ OptionTitle, Value string }{shade, value},
			"another channel's product and a draft name no option value")
	}
}

// withoutEmptyOptions reads an empty option list as none: the REST body leaves
// a number or boolean facet's options out and the schema answers an empty list.
func withoutEmptyOptions(facets []productsvc.Facet) []productsvc.Facet {
	out := slices.Clone(facets)
	for i := range out {
		if len(out[i].Options) == 0 {
			out[i].Options = nil
		}
	}
	return out
}

// idsOf lists the ids of a page.
func idsOf(items []struct{ ID string }) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ID)
	}
	return out
}
