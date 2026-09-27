package service_test

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// attributeFixture is a service with three attributes, one of each kind, and
// three published products holding values of them.
type attributeFixture struct {
	svc                   *service.Service
	store                 *memStore
	shirt, jacket, basket models.Product
}

func newAttributeFixture(t *testing.T) attributeFixture {
	t.Helper()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), &fakeGraph{})
	ctx := context.Background()
	_, err := svc.CreateAttribute(ctx, service.AttributeInput{Title: "Material", Kind: models.AttributeSelect,
		Options: []service.AttributeOptionInput{{Value: "Cotton"}, {Value: "Wool"}, {Value: "Linen"}}})
	require.NoError(t, err)
	_, err = svc.CreateAttribute(ctx, service.AttributeInput{Title: "Width", Kind: models.AttributeNumber, Rank: 1})
	require.NoError(t, err)
	_, err = svc.CreateAttribute(ctx, service.AttributeInput{Title: "Waterproof", Kind: models.AttributeBoolean, Rank: 2})
	require.NoError(t, err)

	fx := attributeFixture{svc: svc, store: store}
	fx.shirt = seedProduct(t, svc, "shirt", "Shirt")
	fx.jacket = seedProduct(t, svc, "jacket", "Jacket")
	fx.basket = seedProduct(t, svc, "basket", "Basket")
	yes, no := true, false
	for product, values := range map[string][]service.ProductAttributeInput{
		fx.shirt.ID: {
			{Attribute: "material", Options: []string{"cotton"}},
			{Attribute: "width", Number: ptr(50.0)}, {Attribute: "waterproof", Boolean: &no},
		},
		fx.jacket.ID: {
			{Attribute: "material", Options: []string{"wool", "cotton"}},
			{Attribute: "width", Number: ptr(70.0)}, {Attribute: "waterproof", Boolean: &yes},
		},
		fx.basket.ID: {{Attribute: "width", Number: ptr(30.5)}},
	} {
		_, err := svc.SetProductAttributes(ctx, product, values)
		require.NoError(t, err)
	}

	return fx
}

// handles lists the handles of a listing's products.
func handles(products []service.StoreProduct) []string {
	out := make([]string, 0, len(products))
	for i := range products {
		out = append(out, products[i].Handle)
	}
	return out
}

// TestAnAttributeIsDefinedWithDerivedHandles is ADR 0219: the handles come from
// the title and the values when left out, and a select keeps its options in
// order.
func TestAnAttributeIsDefinedWithDerivedHandles(t *testing.T) {
	t.Parallel()

	fx := newAttributeFixture(t)
	attributes, err := fx.svc.ListAttributes(context.Background())

	require.NoError(t, err)
	require.Len(t, attributes, 3)
	assert.Equal(t, []string{"material", "width", "waterproof"},
		[]string{attributes[0].Handle, attributes[1].Handle, attributes[2].Handle}, "the operator's order")
	require.Len(t, attributes[0].Options, 3)
	assert.Equal(t, "cotton", attributes[0].Options[0].Handle)
	assert.Equal(t, "Cotton", attributes[0].Options[0].Value)
}

// TestADefinitionOutsideItsKindIsRefused: every refusal is invalid input, and
// nothing is written.
func TestADefinitionOutsideItsKindIsRefused(t *testing.T) {
	t.Parallel()

	fx := newAttributeFixture(t)
	before := fx.store.calls["CreateAttribute"]
	for name, in := range map[string]service.AttributeInput{
		"no title":              {Kind: models.AttributeNumber},
		"no kind":               {Title: "Size"},
		"an unknown kind":       {Title: "Size", Kind: "text"},
		"options on a number":   {Title: "Size", Kind: models.AttributeNumber, Options: []service.AttributeOptionInput{{Value: "S"}}},
		"options on a boolean":  {Title: "Soft", Kind: models.AttributeBoolean, Options: []service.AttributeOptionInput{{Value: "S"}}},
		"a bad handle":          {Title: "Size", Handle: "Size Chart", Kind: models.AttributeNumber},
		"an option given twice": {Title: "Size", Kind: models.AttributeSelect, Options: []service.AttributeOptionInput{{Value: "S"}, {Value: "s"}}},
		"an empty option":       {Title: "Size", Kind: models.AttributeSelect, Options: []service.AttributeOptionInput{{Value: " "}}},
	} {
		_, err := fx.svc.CreateAttribute(context.Background(), in)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), name)
	}
	assert.Equal(t, before, fx.store.calls["CreateAttribute"], "nothing reached the store")

	_, err := fx.svc.AddAttributeOption(context.Background(), attributeID(t, fx, "width"),
		service.AttributeOptionInput{Value: "Wide"})
	assert.True(t, errors.IsInvalid(err), "a number attribute takes no option")
}

// attributeID finds an attribute's id by handle.
func attributeID(t *testing.T, fx attributeFixture, handle string) string {
	t.Helper()

	attributes, err := fx.svc.ListAttributes(context.Background())
	require.NoError(t, err)
	for i := range attributes {
		if attributes[i].Handle == handle {
			return attributes[i].ID
		}
	}
	require.FailNow(t, "no attribute", handle)
	return ""
}

// TestAProductsValuesAreCheckedAgainstTheirKinds: a value of the wrong kind, an
// option the attribute lacks, a number that is not finite and an attribute
// named twice are refused, and the product keeps what it had.
func TestAProductsValuesAreCheckedAgainstTheirKinds(t *testing.T) {
	t.Parallel()

	fx := newAttributeFixture(t)
	ctx := context.Background()
	yes := true
	for name, values := range map[string][]service.ProductAttributeInput{
		"an unknown attribute":      {{Attribute: "shade", Options: []string{"red"}}},
		"an option it lacks":        {{Attribute: "material", Options: []string{"silk"}}},
		"a number for a select":     {{Attribute: "material", Number: ptr(1.0)}},
		"a number with the options": {{Attribute: "material", Options: []string{"cotton"}, Number: ptr(1.0)}},
		"no option for a select":    {{Attribute: "material"}},
		"an option for a number":    {{Attribute: "width", Options: []string{"cotton"}}},
		"a boolean for a number":    {{Attribute: "width", Boolean: &yes}},
		"an infinite number":        {{Attribute: "width", Number: ptr(math.Inf(1))}},
		"no number":                 {{Attribute: "width"}},
		"a number for a boolean":    {{Attribute: "waterproof", Number: ptr(1.0)}},
		"an attribute named twice":  {{Attribute: "width", Number: ptr(1.0)}, {Attribute: "width", Number: ptr(2.0)}},
	} {
		_, err := fx.svc.SetProductAttributes(ctx, fx.basket.ID, values)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), name)
	}

	stored, err := fx.store.ListProductAttributeValues(ctx, []string{fx.basket.ID})
	require.NoError(t, err)
	require.Len(t, stored[fx.basket.ID], 1)
	assert.Equal(t, 30.5, *stored[fx.basket.ID][0].Number, "the product kept what it had")

	values, err := fx.svc.SetProductAttributes(ctx, fx.basket.ID, []service.ProductAttributeInput{
		{Attribute: "material", Options: []string{"linen", "linen"}},
	})
	require.NoError(t, err)
	require.Len(t, values, 1, "the values are replaced whole")
	assert.Len(t, values[0].Options, 1, "an option named twice is chosen once")
}

// TestTheStorefrontFiltersByAttributes: options are ORed within an attribute,
// attributes are ANDed, a range keeps its bounds, and a boolean may arrive as
// the one option true or false.
func TestTheStorefrontFiltersByAttributes(t *testing.T) {
	t.Parallel()

	fx := newAttributeFixture(t)
	ctx := context.Background()
	for name, c := range map[string]struct {
		criteria []service.AttributeCriterion
		want     []string
	}{
		"one option":         {[]service.AttributeCriterion{{Attribute: "material", Options: []string{"wool"}}}, []string{"jacket"}},
		"either option":      {[]service.AttributeCriterion{{Attribute: "material", Options: []string{"cotton", "linen"}}}, []string{"jacket", "shirt"}},
		"a range":            {[]service.AttributeCriterion{{Attribute: "width", Min: ptr(40.0), Max: ptr(60.0)}}, []string{"shirt"}},
		"an open range":      {[]service.AttributeCriterion{{Attribute: "width", Max: ptr(50.0)}}, []string{"basket", "shirt"}},
		"a boolean as text":  {[]service.AttributeCriterion{{Attribute: "waterproof", Options: []string{"true"}}}, []string{"jacket"}},
		"two attributes AND": {[]service.AttributeCriterion{{Attribute: "material", Options: []string{"cotton"}}, {Attribute: "width", Min: ptr(60.0)}}, []string{"jacket"}},
	} {
		result, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{Attributes: c.criteria})
		require.NoError(t, err, name)
		assert.ElementsMatch(t, c.want, handles(result.Items), name)
	}

	for name, criteria := range map[string][]service.AttributeCriterion{
		"an unknown attribute": {{Attribute: "shade", Options: []string{"red"}}},
		"an option it lacks":   {{Attribute: "material", Options: []string{"silk"}}},
		"a range on a select":  {{Attribute: "material", Min: ptr(1.0)}},
		"a range backwards":    {{Attribute: "width", Min: ptr(60.0), Max: ptr(40.0)}},
		"a range not finite":   {{Attribute: "width", Min: ptr(math.NaN())}},
		"text on a boolean":    {{Attribute: "waterproof", Options: []string{"yes"}}},
		"an attribute twice":   {{Attribute: "width", Min: ptr(1.0)}, {Attribute: "width", Max: ptr(2.0)}},
	} {
		_, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{Attributes: criteria})
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), name)
	}
}

// TestAListingFiltersOnAtMostTenAttributes: the bound is on distinct
// attributes, so the fixture defines eleven and names each once.
func TestAListingFiltersOnAtMostTenAttributes(t *testing.T) {
	t.Parallel()

	fx := newAttributeFixture(t)
	ctx := context.Background()
	var criteria []service.AttributeCriterion
	for i := range models.MaxAttributeFilters + 1 {
		handle := fmt.Sprintf("size-%d", i)
		_, err := fx.svc.CreateAttribute(ctx, service.AttributeInput{Handle: handle, Title: "Size", Kind: models.AttributeNumber})
		require.NoError(t, err)
		criteria = append(criteria, service.AttributeCriterion{Attribute: handle, Min: ptr(0.0)})
	}

	_, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{Attributes: criteria})
	assert.True(t, errors.IsInvalid(err), "eleven attributes")
	_, err = fx.svc.ListStoreProducts(ctx, service.StoreListOptions{Attributes: criteria[:models.MaxAttributeFilters]})
	require.NoError(t, err, "ten are taken")
}

// TestTheEnrichedListingKeepsTheAttributeFilter: a listing that filters on
// availability reads the catalog in chunks, and every chunk carries the
// attribute filter too.
func TestTheEnrichedListingKeepsTheAttributeFilter(t *testing.T) {
	t.Parallel()

	fx := newAttributeFixture(t)
	result, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		InStock: ptr(false), Attributes: []service.AttributeCriterion{{Attribute: "material", Options: []string{"wool"}}},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"jacket"}, handles(result.Items), "no product is stocked, and wool is the jacket's alone")
}

// TestAFacetIsCountedWithoutItsOwnFilter: with material filtered to cotton,
// the material facet still counts wool, while width and waterproof count only
// what cotton leaves.
func TestAFacetIsCountedWithoutItsOwnFilter(t *testing.T) {
	t.Parallel()

	fx := newAttributeFixture(t)
	facets, err := fx.svc.StoreFacets(context.Background(), service.StoreListOptions{
		Attributes: []service.AttributeCriterion{{Attribute: "material", Options: []string{"cotton"}}},
	})

	require.NoError(t, err)
	require.Len(t, facets, 3)
	material, width, waterproof := facets[0], facets[1], facets[2]
	assert.Equal(t, []service.FacetOption{
		{Handle: "cotton", Value: "Cotton", Products: 2},
		{Handle: "linen", Value: "Linen", Products: 0},
		{Handle: "wool", Value: "Wool", Products: 1},
	}, material.Options, "the filtered attribute counts its other options too, in rank then handle order")
	assert.Equal(t, int64(2), width.Products, "shirt and jacket, not the basket that has no cotton")
	assert.Equal(t, 50.0, *width.Min)
	assert.Equal(t, 70.0, *width.Max)
	assert.Equal(t, []int64{1, 1}, []int64{waterproof.True, waterproof.False})
	assert.Equal(t, 2, fx.store.calls["AttributeFacets"], "one read for the filtered attribute, one for the rest")

	_, err = fx.svc.StoreFacets(context.Background(), service.StoreListOptions{InStock: ptr(true)})
	assert.True(t, errors.IsInvalid(err), "availability is not a catalog column")
}
