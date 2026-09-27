package api_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/api"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// attributeCatalog records what the attribute handlers hand the service.
type attributeCatalog struct {
	api.Catalog

	listed       service.StoreListOptions
	counted      service.StoreListOptions
	setProduct   string
	setValues    []service.ProductAttributeInput
	createdInput service.AttributeInput
}

func (c *attributeCatalog) ListStoreProducts(
	_ context.Context, opts service.StoreListOptions,
) (service.ListResult[service.StoreProduct], error) {
	c.listed = opts
	return service.ListResult[service.StoreProduct]{}, nil
}

func (c *attributeCatalog) StoreFacets(_ context.Context, opts service.StoreListOptions) ([]service.Facet, error) {
	c.counted = opts
	return []service.Facet{{Handle: "material", Title: "Material", Kind: models.AttributeSelect, Products: 2}}, nil
}

func (c *attributeCatalog) SetProductAttributes(
	_ context.Context, id string, values []service.ProductAttributeInput,
) ([]models.ProductAttributeValue, error) {
	c.setProduct, c.setValues = id, values
	return nil, nil
}

func (c *attributeCatalog) CreateAttribute(_ context.Context, in service.AttributeInput) (models.Attribute, error) {
	c.createdInput = in
	return models.Attribute{ID: "pattr_1", Handle: "material"}, nil
}

// withAttributes is the listing's address with the given attribute filters.
func withAttributes(path string, values ...string) string {
	query := url.Values{"attribute": values}
	return path + "?" + query.Encode()
}

// channelKey is a publishable key bound to the scoped channel.
var channelKey = &corehttp.Principal{ID: "pk_1", Kind: "publishable_key", SalesChannelIDs: []string{scopedChannel}}

// TestTheAttributeParameterIsRead is ADR 0219's query string: options split on
// commas, a range with either end open, and a boolean left for the service to
// read by the attribute's kind.
func TestTheAttributeParameterIsRead(t *testing.T) {
	t.Parallel()

	catalog := &attributeCatalog{}
	rec := storeRequest(t, newRouter(catalog), withAttributes(storeProductsPath,
		"material:cotton, wool", "width:10..", "height:..2.5", "waterproof:true"), channelKey)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	ten, twoAndHalf := 10.0, 2.5
	assert.Equal(t, []service.AttributeCriterion{
		{Attribute: "material", Options: []string{"cotton", "wool"}},
		{Attribute: "width", Min: &ten},
		{Attribute: "height", Max: &twoAndHalf},
		{Attribute: "waterproof", Options: []string{"true"}},
	}, catalog.listed.Attributes)
}

// TestAnAttributeParameterOutOfShapeIsRefused: the service is not asked.
func TestAnAttributeParameterOutOfShapeIsRefused(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"material", "material:", ":cotton", "width:ten..20", "width:1..x"} {
		catalog := &attributeCatalog{}
		rec := storeRequest(t, newRouter(catalog), withAttributes(storeProductsPath, value), channelKey)

		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, value)
		assert.Nil(t, catalog.listed.Attributes, value)
	}
}

// TestTheFacetsTakeTheListingsFiltersAndChannel: the facet count reads the same
// catalog filters from the query string and the channel from the path.
func TestTheFacetsTakeTheListingsFiltersAndChannel(t *testing.T) {
	t.Parallel()

	catalog := &attributeCatalog{}
	rec := storeRequest(t, newRouter(catalog),
		"/store/v1/sales-channels/sc_a/product-facets?collection_id=pcol_1&q=shirt&attribute=material:cotton",
		channelKey)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{scopedChannel}, catalog.counted.SalesChannelIDs)
	require.NotNil(t, catalog.counted.CollectionID)
	assert.Equal(t, "pcol_1", *catalog.counted.CollectionID)
	assert.Equal(t, "shirt", *catalog.counted.Search)
	assert.Equal(t, []service.AttributeCriterion{{Attribute: "material", Options: []string{"cotton"}}},
		catalog.counted.Attributes)
	assert.Contains(t, rec.Body.String(), `"handle":"material"`)

	other := storeRequest(t, newRouter(&attributeCatalog{}),
		"/store/v1/sales-channels/sc_other/product-facets", channelKey)
	assert.Equal(t, http.StatusForbidden, other.Code, "a channel the key does not hold")
}

// TestAProductsAttributesAreReplacedFromTheBody: the route hands the service
// the product and the values as written.
func TestAProductsAttributesAreReplacedFromTheBody(t *testing.T) {
	t.Parallel()

	catalog := &attributeCatalog{}
	rec := do(t, newRouter(catalog), http.MethodPut, "/admin/v1/products/prod_1/attributes",
		`{"values":[{"attribute":"material","options":["cotton"]},{"attribute":"width","number":2.5},
		{"attribute":"waterproof","boolean":true}]}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "prod_1", catalog.setProduct)
	require.Len(t, catalog.setValues, 3)
	assert.Equal(t, []string{"cotton"}, catalog.setValues[0].Options)
	assert.InDelta(t, 2.5, *catalog.setValues[1].Number, 0)
	assert.True(t, *catalog.setValues[2].Boolean)
	assert.Equal(t, `{"data":[]}`+"\n", rec.Body.String(), "no values is an empty list, not null")
}

// TestAnAttributeIsDefinedFromTheBody: the definition and its options reach the
// service as written.
func TestAnAttributeIsDefinedFromTheBody(t *testing.T) {
	t.Parallel()

	catalog := &attributeCatalog{}
	rec := do(t, newRouter(catalog), http.MethodPost, "/admin/v1/product-attributes",
		`{"title":"Material","kind":"select","rank":2,"options":[{"value":"Cotton","handle":"cotton","rank":1}]}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, service.AttributeInput{Title: "Material", Kind: models.AttributeSelect, Rank: 2,
		Options: []service.AttributeOptionInput{{Value: "Cotton", Handle: "cotton", Rank: 1}}}, catalog.createdInput)
}
