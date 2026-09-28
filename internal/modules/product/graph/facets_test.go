package graph_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestTheFacetsAreTheRESTReadsOwn is ADR 0226: productFacets hands every
// filter it is given to the REST facet read's own method, trimmed, with the
// channels of the request's identity and nothing else, and answers each kind
// of facet as the service counted it.
func TestTheFacetsAreTheRESTReadsOwn(t *testing.T) {
	t.Parallel()

	low, high := 100.0, 120.0
	fake := &fakeStorefront{facets: []service.Facet{
		{
			Handle: "material", Title: "Material", Kind: models.AttributeSelect, Products: 3,
			Options: []service.FacetOption{
				{Handle: "cotton", Value: "Cotton", Products: 2},
				{Handle: "wool", Value: "Wool", Products: 1},
			},
		},
		{Handle: "organic", Title: "Organic", Kind: models.AttributeBoolean, True: 4, False: 1, Products: 5},
		{Handle: "chest", Title: "Chest", Kind: models.AttributeNumber, Products: 2, Min: &low, Max: &high},
	}}

	response, status := runQuery(t, identityWith([]string{"sc_1", "sc_2"}), fake, `{
		productFacets(
			q: " linen ", collectionId: " pcol_1 ", categoryId: "pcat_1", tagId: "ptag_1",
			optionValue: "red", variantIds: ["pvar_1"],
			attributes: [{attribute: "material", options: ["cotton"]}]
		) { handle title kind options { handle value products } true false products min max }
	}`)

	require.Equal(t, http.StatusOK, status)
	require.Empty(t, response.Errors)
	assert.Equal(t, service.StoreListOptions{
		CollectionID:    ptr("pcol_1"),
		CategoryID:      ptr("pcat_1"),
		TagID:           ptr("ptag_1"),
		OptionValue:     ptr("red"),
		VariantIDs:      []string{"pvar_1"},
		Attributes:      []service.AttributeCriterion{{Attribute: "material", Options: []string{"cotton"}}},
		Search:          ptr("linen"),
		SalesChannelIDs: []string{"sc_1", "sc_2"},
	}, fake.lastFacets(t))

	facet := func(handle, title, kind string, options []any, yes, no, products float64, low, high any) any {
		return map[string]any{
			"handle": handle, "title": title, "kind": kind, "options": options,
			"true": yes, "false": no, "products": products, "min": low, "max": high,
		}
	}
	assert.Equal(t, []any{
		facet("material", "Material", "select", []any{
			map[string]any{"handle": "cotton", "value": "Cotton", "products": float64(2)},
			map[string]any{"handle": "wool", "value": "Wool", "products": float64(1)},
		}, 0, 0, 3, nil, nil),
		facet("organic", "Organic", "boolean", []any{}, 4, 1, 5, nil, nil),
		facet("chest", "Chest", "number", []any{}, 0, 0, 2, 100.0, 120.0),
	}, response.Data["productFacets"])
}

// TestTheOptionValuesAreTheRESTReadsOwn is ADR 0226: optionValues asks the
// REST option vocabulary's method for the published products' values in the
// request's channels, one page at a time, and answers the page it returns.
func TestTheOptionValuesAreTheRESTReadsOwn(t *testing.T) {
	t.Parallel()

	fake := &fakeStorefront{optionValues: []models.OptionValuePair{
		{OptionTitle: "Color", Value: "red"},
		{OptionTitle: "Size", Value: "M"},
	}}

	response, status := runQuery(t, identityWith([]string{"sc_1"}), fake,
		`{ optionValues(limit: 5, offset: 2) { items { optionTitle value } count limit offset } }`)

	require.Equal(t, http.StatusOK, status)
	require.Empty(t, response.Errors)
	assert.Equal(t, []service.ListOptionValuesOptions{{
		SalesChannelIDs: []string{"sc_1"}, PublicOnly: true, Limit: 5, Offset: 2,
	}}, fake.optionValueOptions)
	assert.Equal(t, map[string]any{
		"items": []any{
			map[string]any{"optionTitle": "Color", "value": "red"},
			map[string]any{"optionTitle": "Size", "value": "M"},
		},
		"count": float64(2), "limit": float64(5), "offset": float64(2),
	}, response.Data["optionValues"])
}

// TestTheFacetsArePricedByTheirRoundTrips holds that every attribute the
// filters name is charged as a root query, since the count makes a round trip
// for each: the same selection passes with none and is refused with one,
// before the service is asked.
func TestTheFacetsArePricedByTheirRoundTrips(t *testing.T) {
	t.Parallel()

	// One facet field is 1 per attribute, 100 attributes; the root is 1,000.
	opts := graph.Options{MaxComplexity: 1500}

	cheap := &fakeStorefront{}
	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), cheap,
		`{ productFacets { handle } }`, opts)
	require.Empty(t, response.Errors)
	require.Len(t, cheap.facetOptions, 1)

	expensive := &fakeStorefront{}
	response, _ = runQueryWithOptions(t, identityWith([]string{"sc_1"}), expensive,
		`{ productFacets(attributes: [{attribute: "material", options: ["cotton"]}]) { handle } }`, opts)
	require.NotEmpty(t, response.Errors, "a filtered attribute is a round trip of its own")
	assert.Contains(t, response.Errors[0].Message, "complexity")
	assert.Empty(t, expensive.facetOptions)
}
