package graph_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestAddOnsAskForTheirProductWithTheIdentitysChannels is ADR 0231: the field
// asks the REST read's method for the product it hangs off, by id, with the
// channels of the request's identity, and answers each add-on's variant and
// product as the service returned them.
func TestAddOnsAskForTheirProductWithTheIdentitysChannels(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{
		single: service.StoreProduct{Product: models.Product{ID: "prod_1", Handle: "ring"}},
		addOns: []service.StoreAddOn{
			{VariantID: "variant_engraving", Product: service.StoreProduct{Product: models.Product{Handle: "engraving"}}},
			{VariantID: "variant_wrap", Product: service.StoreProduct{Product: models.Product{Handle: "wrap"}}},
		},
	}

	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc,
		`{ product(handle: "ring") { addOns { variantId product { handle } } } }`)

	require.Empty(t, response.Errors)
	assert.Equal(t, []relatedCall{{parent: "prod_1", channels: []string{"sc_1"}}}, svc.addOnCalls)
	data, err := json.Marshal(response.Data)
	require.NoError(t, err)
	assert.JSONEq(t, `{"product":{"addOns":[
		{"variantId":"variant_engraving","product":{"handle":"engraving"}},
		{"variantId":"variant_wrap","product":{"handle":"wrap"}}
	]}}`, string(data))
}

// TestAddOnsArePricedAsARoundTrip holds the field's price (ADR 0231): one
// product's add-ons pass the default ceiling, and a page of fifty products each
// asking for theirs is refused before any read, as the related products are.
func TestAddOnsArePricedAsARoundTrip(t *testing.T) {
	t.Parallel()

	one := &fakeStorefront{single: service.StoreProduct{Product: models.Product{ID: "prod_1"}}}
	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), one,
		`{ product(handle: "ring") { addOns { variantId } } }`, graph.Options{})
	require.Empty(t, response.Errors)

	page := &fakeStorefront{}
	response, _ = runQueryWithOptions(t, identityWith([]string{"sc_1"}), page,
		`{ products(limit: 50) { items { addOns { variantId } } } }`, graph.Options{})
	require.NotEmpty(t, response.Errors, "fifty reads of add-ons cost fifty round trips")
	assert.Contains(t, response.Errors[0].Message, "complexity")
	assert.Empty(t, page.listOptions)
	assert.Empty(t, page.addOnCalls)
}
