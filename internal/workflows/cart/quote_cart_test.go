package cart

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// measuredCatalog answers the quote's measure reads, the ones naming
// [FieldWeight], and hands every other read on.
type measuredCatalog struct {
	Catalog
	variants map[string]query.Record
	products map[string]query.Record
	err      error
}

func (c *measuredCatalog) Graph(ctx context.Context, spec query.GraphSpec) ([]query.Record, error) {
	if !slices.Contains(spec.Fields, FieldWeight) {
		return c.Catalog.Graph(ctx, spec)
	}
	if c.err != nil {
		return nil, c.err
	}
	source := c.variants
	if spec.Entity == EntityProduct {
		source = c.products
	}
	ids, _ := spec.Filters[FilterIDs].([]string)
	out := []query.Record{}
	for _, id := range ids {
		if record, ok := source[id]; ok {
			out = append(out, record)
		}
	}

	return out, nil
}

// TestAQuoteNamesTheCartItPrices is ADR 0445: the request carries the cart's
// traveling lines, priced as the round priced them and measured as the
// catalog records them, each measure under the record that holds it.
func TestAQuoteNamesTheCartItPrices(t *testing.T) {
	h := shippingHarness(t)
	installProductCatalog(h, map[string]productFacts{
		testProductA: {Discountable: true},
		testProductB: {IsGiftcard: true},
	})
	h.carts.snapshotFn = giftCardCart
	h.wf.catalog = &measuredCatalog{
		Catalog: h.wf.catalog,
		variants: map[string]query.Record{
			testVariantA: {query.IDField: testVariantA, FieldProductID: testProductA, FieldWeight: int32(0)},
			testVariantB: {query.IDField: testVariantB, FieldProductID: testProductB, FieldWeight: int32(5)},
		},
		products: map[string]query.Record{
			testProductA: {
				query.IDField: testProductA, FieldWeight: int32(1200),
				FieldLength: int32(30), FieldWidth: int32(20), FieldHeight: int32(10),
			},
		},
	}

	_, err := h.wf.AddQuotedShippingMethod(context.Background(), testCartID, "so_1", nil)
	require.NoError(t, err)

	cart := h.shipping.gotRequest.Cart
	require.NotNil(t, cart, "a quote asked for a cart names it")
	assert.Equal(t, testCartID, cart.ID)
	assert.Equal(t, testCurrency, cart.CurrencyCode)
	require.Len(t, cart.Lines, 1, "the gift card line does not travel")
	assert.Equal(t, quoteLine{
		ID: "cli_2", VariantID: testVariantA, ProductID: testProductA, Quantity: 2,
		UnitPrice: 1000, Subtotal: 2000,
		ProductWeight: 1200, ProductLength: 30, ProductWidth: 20, ProductHeight: 10,
	}, cart.Lines[0])
}

// TestACartWhoseGoodsCannotBeReadIsQuotedWithoutIt holds the failed read to
// the rule the facts follow: the quote is given, and it names no cart.
func TestACartWhoseGoodsCannotBeReadIsQuotedWithoutIt(t *testing.T) {
	h := shippingHarness(t)
	h.wf.catalog = &measuredCatalog{
		Catalog: h.wf.catalog,
		err:     coreerrors.Unavailable("query_unavailable", "the read layer is unreachable"),
	}

	_, err := h.wf.AddQuotedShippingMethod(context.Background(), testCartID, "so_1", nil)
	require.NoError(t, err)

	require.Equal(t, 1, h.shipping.calls)
	assert.Nil(t, h.shipping.gotRequest.Cart)
}
