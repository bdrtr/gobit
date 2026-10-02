package review

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// catalogOf answers the variants of each product it knows.
type catalogOf struct {
	variants map[string][]string
	err      error
	asked    query.GraphSpec
}

func (c *catalogOf) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	c.asked = spec
	if c.err != nil {
		return nil, c.err
	}
	productID, _ := spec.Filters[filterProductID].(string)
	records := []query.Record{}
	for _, id := range c.variants[productID] {
		records = append(records, query.Record{query.IDField: id})
	}

	return records, nil
}

// ordersOf answers which variants each customer bought, and what it was asked.
type ordersOf struct {
	bought map[string][]string
	asked  []string
}

func (o *ordersOf) CustomerBoughtAnyOf(_ context.Context, customerID string, variantIDs []string) (bool, error) {
	o.asked = variantIDs
	for _, bought := range o.bought[customerID] {
		for _, id := range variantIDs {
			if bought == id {
				return true, nil
			}
		}
	}

	return false, nil
}

// purchasesOver builds the module's purchases over a container holding what
// is given, its log written to the buffer.
func purchasesOver(t *testing.T, logged *bytes.Buffer, provide map[string]any) *purchases {
	t.Helper()

	c := container.New(nil)
	for name, value := range provide {
		require.NoError(t, c.Provide(name, value))
	}

	return &purchases{c: c, log: slog.New(slog.NewTextHandler(logged, nil))}
}

// TestAPurchaseIsReadFromTheProductsVariants is ADR 0372's read: the
// product's variants from the catalog, then whether the customer's orders
// carry one of them.
func TestAPurchaseIsReadFromTheProductsVariants(t *testing.T) {
	t.Parallel()

	catalog := &catalogOf{variants: map[string][]string{"prod_1": {"variant_250", "variant_1000"}, "prod_empty": nil}}
	orders := &ordersOf{bought: map[string][]string{"cust_BUYER": {"variant_1000"}}}
	p := purchasesOver(t, &bytes.Buffer{}, map[string]any{svcQuery: query.Query(catalog), orderInteropName: orders})
	ctx := context.Background()

	bought, err := p.Bought(ctx, "cust_BUYER", "prod_1")
	require.NoError(t, err)
	assert.True(t, bought, "a buyer of one of the product's variants")
	assert.Equal(t, entityVariant, catalog.asked.Entity)
	assert.Equal(t, []string{"variant_250", "variant_1000"}, orders.asked, "every variant of the product is asked")

	bought, err = p.Bought(ctx, "cust_BROWSER", "prod_1")
	require.NoError(t, err)
	assert.False(t, bought)

	orders.asked = nil
	bought, err = p.Bought(ctx, "cust_BUYER", "prod_empty")
	require.NoError(t, err)
	assert.False(t, bought, "a product with no variants was bought by nobody")
	assert.Nil(t, orders.asked, "and the orders are not asked")
}

// TestAPurchaseWithoutTheOrderModuleIsNoPurchase: a composition without the
// order module writes every review unverified, and warns once.
func TestAPurchaseWithoutTheOrderModuleIsNoPurchase(t *testing.T) {
	t.Parallel()

	var logged bytes.Buffer
	p := purchasesOver(t, &logged, map[string]any{svcQuery: query.Query(&catalogOf{})})

	for range 2 {
		bought, err := p.Bought(context.Background(), "cust_BUYER", "prod_1")
		require.NoError(t, err)
		assert.False(t, bought)
	}
	assert.Equal(t, 1, bytes.Count(logged.Bytes(), []byte("level=WARN")), "warned once: %s", logged.String())
	assert.Contains(t, logged.String(), orderInteropName)
}

// TestAPurchaseThatCannotBeReadIsAnError: a catalog failure, and a surface
// registered under the wrong type, end the submission.
func TestAPurchaseThatCannotBeReadIsAnError(t *testing.T) {
	t.Parallel()

	down := purchasesOver(t, &bytes.Buffer{}, map[string]any{
		svcQuery:         query.Query(&catalogOf{err: errors.Unavailable("db_down", "no answer")}),
		orderInteropName: &ordersOf{},
	})
	_, err := down.Bought(context.Background(), "cust_BUYER", "prod_1")
	require.Error(t, err)
	assert.Equal(t, codePurchaseReadFailed, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "the catalog's kind is kept: %v", err)

	wrong := purchasesOver(t, &bytes.Buffer{}, map[string]any{
		svcQuery: query.Query(&catalogOf{}), orderInteropName: "not the order module",
	})
	_, err = wrong.Bought(context.Background(), "cust_BUYER", "prod_1")
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal), "%v", err)
	assert.Equal(t, codeSetupFailed, errors.CodeOf(err))
}
