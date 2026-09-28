package graph_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestTheVocabularyIsTheRESTReadsOwn is ADR 0225: the four root queries call
// the storefront REST reads' methods with the document's arguments, the
// categories only ever the public ones, and answer what they return.
func TestTheVocabularyIsTheRESTReadsOwn(t *testing.T) {
	parent := "pcat_parent"
	fake := &fakeStorefront{
		collections: []models.Collection{{ID: "pcol_1", Title: "Summer", Handle: "summer"}},
		categories:  []models.Category{{ID: "pcat_1", Name: "Tops", Handle: "tops", ParentID: &parent}},
		tags:        []models.Tag{{ID: "ptag_1", Value: "linen"}},
		attributes: []models.Attribute{{
			ID: "pattr_1", Handle: "material", Title: "Material", Kind: models.AttributeSelect, Rank: 2,
			Options: []models.AttributeOption{{Handle: "cotton", Value: "Cotton"}, {Handle: "wool", Value: "Wool"}},
		}},
	}

	response, status := runQuery(t, identityWith([]string{"sc_1"}), fake, `{
		collections(limit: 5, offset: 1) { items { id title handle } count limit offset }
		categories(parentId: "pcat_parent", limit: 7) { items { id name parentId } count }
		tags(limit: 3) { items { id value } count }
		productAttributes { handle kind rank options { handle value } }
	}`)

	require.Equal(t, http.StatusOK, status)
	require.Empty(t, response.Errors)
	require.Len(t, fake.categoryOptions, 1)
	asked := fake.categoryOptions[0]
	assert.True(t, asked.PublicOnly, "the storefront never lists an internal or inactive category")
	require.NotNil(t, asked.ParentID)
	assert.Equal(t, "pcat_parent", *asked.ParentID)
	assert.Equal(t, 7, asked.Limit)
	assert.ElementsMatch(t, [][2]int{{5, 1}, {3, 0}}, fake.vocabularyPages)

	data := response.Data
	collections, ok := data["collections"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{map[string]any{"id": "pcol_1", "title": "Summer", "handle": "summer"}}, collections["items"])
	assert.InDelta(t, 1, collections["count"], 0)
	categories, ok := data["categories"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{map[string]any{"id": "pcat_1", "name": "Tops", "parentId": "pcat_parent"}}, categories["items"])
	tags, ok := data["tags"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{map[string]any{"id": "ptag_1", "value": "linen"}}, tags["items"])
	assert.Equal(t, []any{map[string]any{
		"handle": "material", "kind": "select", "rank": float64(2),
		"options": []any{
			map[string]any{"handle": "cotton", "value": "Cotton"},
			map[string]any{"handle": "wool", "value": "Wool"},
		},
	}}, data["productAttributes"])
}

// TestTheVocabularyIsPricedByItsPage holds the reason its pages' items carry
// no cost of their own: each query is multiplied by the page it asks for, so
// the same document asking for a hundred times the rows is refused before the
// service is asked.
func TestTheVocabularyIsPricedByItsPage(t *testing.T) {
	t.Parallel()

	const ceiling = 1200
	for name, selection := range map[string]string{
		"collections": `{ items { id title handle } }`,
		"categories":  `{ items { id name handle } }`,
		"tags":        `{ items { id value } }`,
		// ADR 0226's vocabulary pages as the other three do.
		"optionValues": `{ items { optionTitle value } }`,
	} {
		cheap := &fakeStorefront{}
		response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), cheap,
			"{ "+name+"(limit: 1) "+selection+" }", graph.Options{MaxComplexity: ceiling})
		require.Empty(t, response.Errors, name)

		expensive := &fakeStorefront{}
		response, _ = runQueryWithOptions(t, identityWith([]string{"sc_1"}), expensive,
			"{ "+name+"(limit: 100) "+selection+" }", graph.Options{MaxComplexity: ceiling})
		require.NotEmpty(t, response.Errors, "%s: a hundred rows cost a hundred times one", name)
		assert.Contains(t, response.Errors[0].Message, "complexity", name)
		assert.Empty(t, expensive.vocabularyPages, name)
		assert.Empty(t, expensive.categoryOptions, name)
		assert.Empty(t, expensive.optionValueOptions, name)
	}
}

// TestAFailedVocabularyReadIsAnError holds that a read the service could not
// make is answered as an error with its code, never as an empty page a
// storefront would print as a shop with no collections.
func TestAFailedVocabularyReadIsAnError(t *testing.T) {
	t.Parallel()

	for name, query := range map[string]string{
		"collections":       `{ collections { items { id } } }`,
		"categories":        `{ categories { items { id } } }`,
		"tags":              `{ tags { items { id } } }`,
		"productAttributes": `{ productAttributes { id } }`,
		// The two channel-scoped reads (ADR 0226).
		"productFacets": `{ productFacets { handle } }`,
		"optionValues":  `{ optionValues { items { value } } }`,
	} {
		svc := &fakeStorefront{
			tags:         []models.Tag{{ID: "ptag_1", Value: "linen"}},
			facets:       []service.Facet{{Handle: "material"}},
			optionValues: []models.OptionValuePair{{OptionTitle: "Color", Value: "red"}},
			err:          coreerrors.Unavailable("catalog_unavailable", "the catalog cannot be read"),
		}
		response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc, query)

		require.NotEmpty(t, response.Errors, name)
		assert.Equal(t, "catalog_unavailable", response.Errors[0].Extensions["code"], name)
		assert.Nil(t, response.Data[name], name)
	}
}
