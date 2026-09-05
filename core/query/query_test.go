package query_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/query"
)

// The link definitions used in the tests. The names and ends stay faithful to
// the "important links" list in plan Section 6.
var (
	// productVariant links a product to its variants: one product, many variants.
	productVariant = link.LinkDefinition{
		Name:        "product_variant",
		From:        link.LinkSide{Module: "product", Field: "product_id"},
		To:          link.LinkSide{Module: "variant", Field: "variant_id"},
		Cardinality: link.OneToMany,
	}
	// variantPrice links a variant to a single price set.
	variantPrice = link.LinkDefinition{
		Name:        "variant_price",
		From:        link.LinkSide{Module: "variant", Field: "variant_id"},
		To:          link.LinkSide{Module: "pricing", Field: "price_set_id"},
		Cardinality: link.OneToOne,
	}
	// productChannel links a product to sales channels: many to many.
	productChannel = link.LinkDefinition{
		Name:        "product_channel",
		From:        link.LinkSide{Module: "product", Field: "product_id"},
		To:          link.LinkSide{Module: "channel", Field: "channel_id"},
		Cardinality: link.ManyToMany,
	}
)

// --- fetching the root ------------------------------------------------------

func TestGraphFetchesTheRootRecordsAndPassesTheFieldSelection(t *testing.T) {
	products := newProvider("product",
		query.Record{"id": "prod_1", "title": "Red T-shirt", "hidden": "a"},
		query.Record{"id": "prod_2", "title": "Blue T-shirt", "hidden": "b"},
		query.Record{"id": "prod_3", "title": "Green T-shirt", "hidden": "c"},
	)
	q := query.New(newLinks(), newContainer(t, products), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity:  "product",
		Fields:  []string{"title"},
		Filters: map[string]any{"status": "published"},
		Limit:   1,
		Offset:  1,
	})
	require.NoError(t, err)

	require.Len(t, got, 1)
	assert.Equal(t, query.Record{"title": "Blue T-shirt"}, got[0])

	opts := products.opts()
	assert.Equal(t, []string{"title"}, opts.Fields,
		"with no expansion the id field must not be added")
	assert.Equal(t, map[string]any{"status": "published"}, opts.Filters)
	assert.Equal(t, 1, opts.Limit)
	assert.Equal(t, 1, opts.Offset)
	assert.Equal(t, providerCalls{list: 1}, products.calls())
}

func TestGraphReturnsAnEmptySliceWithNoRootRecord(t *testing.T) {
	products := newProvider("product")
	q := query.New(newLinks(), newContainer(t, products), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product"})
	require.NoError(t, err)
	require.NotNil(t, got, "with no root record an empty slice has to come back, not nil")
	assert.Empty(t, got)
}

func TestGraphAddsTheIDFieldWhenThereIsAnExpansion(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1", "title": "T-shirt"})
	prices := newProvider("pricing", query.Record{"id": "pset_1", "amount": 1990})

	links := newLinks(variantPrice, link.LinkDefinition{
		Name:        "product_price",
		From:        link.LinkSide{Module: "product", Field: "product_id"},
		To:          link.LinkSide{Module: "pricing", Field: "price_set_id"},
		Cardinality: link.OneToOne,
	})
	links.connect("product_price", "prod_1", "pset_1")

	q := query.New(links, newContainer(t, products, prices), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Fields: []string{"title"},
		Expand: []query.Expansion{{Link: "product_price", Fields: []string{"amount"}}},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"title", "id"}, products.opts().Fields,
		"the id field has to be added to the root field list for the join")

	_, fields := prices.fetchArgs()
	assert.Equal(t, []string{"amount", "id"}, fields,
		"the id field has to be added to the expansion field list for the join too")

	require.Len(t, got, 1)
	assert.Equal(t, "prod_1", got[0]["id"])
	price, ok := got[0]["product_price"].(query.Record)
	require.True(t, ok)
	assert.Equal(t, query.Record{"id": "pset_1", "amount": 1990}, price)
}

// --- cardinality and shape --------------------------------------------------

func TestGraphOneToOneWritesASingleRecord(t *testing.T) {
	variants := newProvider("variant", query.Record{"id": "var_1", "sku": "TS-M"})
	prices := newProvider("pricing", query.Record{"id": "pset_1", "amount": 1990})

	links := newLinks(variantPrice).connect("variant_price", "var_1", "pset_1")
	q := query.New(links, newContainer(t, variants, prices), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "variant",
		Expand: []query.Expansion{{Link: "variant_price"}},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)

	price, ok := got[0]["variant_price"].(query.Record)
	require.Truef(t, ok, "a OneToOne expansion has to write a single record; the type that arrived: %T", got[0]["variant_price"])
	assert.Equal(t, "pset_1", price["id"])
}

func TestGraphOneToManyWritesASlice(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	variants := newProvider("variant",
		query.Record{"id": "var_1", "sku": "TS-S"},
		query.Record{"id": "var_2", "sku": "TS-M"},
	)

	links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1", "var_2")
	q := query.New(links, newContainer(t, products, variants), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)

	list, ok := got[0]["product_variant"].([]query.Record)
	require.Truef(t, ok, "a OneToMany expansion has to write a slice; the type that arrived: %T", got[0]["product_variant"])
	require.Len(t, list, 2)
	assert.Equal(t, "var_1", list[0]["id"])
	assert.Equal(t, "var_2", list[1]["id"])
}

func TestGraphManyToManyWritesASlice(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	channels := newProvider("channel",
		query.Record{"id": "sc_web"},
		query.Record{"id": "sc_pos"},
	)

	links := newLinks(productChannel).connect("product_channel", "prod_1", "sc_web", "sc_pos")
	q := query.New(links, newContainer(t, products, channels), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_channel"}},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)

	list, ok := got[0]["product_channel"].([]query.Record)
	require.Truef(t, ok, "a ManyToMany expansion has to write a slice; the type that arrived: %T", got[0]["product_channel"])
	assert.Len(t, list, 2)
}

func TestGraphKeepsTheShapeWithNoMatch(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	variants := newProvider("variant")
	prices := newProvider("pricing")

	links := newLinks(productVariant, link.LinkDefinition{
		Name:        "product_price",
		From:        link.LinkSide{Module: "product", Field: "product_id"},
		To:          link.LinkSide{Module: "pricing", Field: "price_set_id"},
		Cardinality: link.OneToOne,
	})
	q := query.New(links, newContainer(t, products, variants, prices), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{
			{Link: "product_variant"},
			{Link: "product_price"},
		},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)

	list, ok := got[0]["product_variant"].([]query.Record)
	require.Truef(t, ok, "a many-ended expansion has to write a slice even with no match; the type that arrived: %T",
		got[0]["product_variant"])
	assert.Empty(t, list, "the slice has to be empty but not nil")
	assert.NotNil(t, list)

	require.Contains(t, got[0], "product_price")
	assert.Nil(t, got[0]["product_price"], "a single-ended expansion has to write nil with no match")

	assert.Zero(t, variants.calls().fetch, "with no related id the provider must not be reached at all")
	assert.Zero(t, prices.calls().fetch, "with no related id the provider must not be reached at all")
}

// --- the output key ---------------------------------------------------------

func TestGraphUsesTheLinkNameAsTheKeyWhenAsIsEmpty(t *testing.T) {
	variants := newProvider("variant", query.Record{"id": "var_1"})
	prices := newProvider("pricing", query.Record{"id": "pset_1"})

	links := newLinks(variantPrice).connect("variant_price", "var_1", "pset_1")
	q := query.New(links, newContainer(t, variants, prices), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "variant",
		Expand: []query.Expansion{{Link: "variant_price"}},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "variant_price", "with As empty the key has to be the link name")
}

func TestGraphUsesAsAsTheKeyWhenItIsSet(t *testing.T) {
	variants := newProvider("variant", query.Record{"id": "var_1"})
	prices := newProvider("pricing", query.Record{"id": "pset_1"})

	links := newLinks(variantPrice).connect("variant_price", "var_1", "pset_1")
	q := query.New(links, newContainer(t, variants, prices), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "variant",
		Expand: []query.Expansion{{Link: "variant_price", As: "price"}},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "price")
	assert.NotContains(t, got[0], "variant_price", "with As given the link name must not be the key")
}

// --- nested expansion -------------------------------------------------------

func TestGraphNestedExpansionTwoLevels(t *testing.T) {
	products := newProvider("product",
		query.Record{"id": "prod_1", "title": "T-shirt"},
		query.Record{"id": "prod_2", "title": "Hat"},
	)
	variants := newProvider("variant",
		query.Record{"id": "var_1", "sku": "TS-S"},
		query.Record{"id": "var_2", "sku": "TS-M"},
		query.Record{"id": "var_3", "sku": "SP-U"},
	)
	prices := newProvider("pricing",
		query.Record{"id": "pset_1", "amount": 1990},
		query.Record{"id": "pset_3", "amount": 2990},
	)

	links := newLinks(productVariant, variantPrice)
	links.connect("product_variant", "prod_1", "var_1", "var_2")
	links.connect("product_variant", "prod_2", "var_3")
	links.connect("variant_price", "var_1", "pset_1")
	links.connect("variant_price", "var_3", "pset_3")

	q := query.New(links, newContainer(t, products, variants, prices), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{
			Link:   "product_variant",
			As:     "varyantlar",
			Expand: []query.Expansion{{Link: "variant_price", As: "price"}},
		}},
	})
	require.NoError(t, err)
	require.Len(t, got, 2)

	first, ok := got[0]["varyantlar"].([]query.Record)
	require.True(t, ok)
	require.Len(t, first, 2)

	price, ok := first[0]["price"].(query.Record)
	require.Truef(t, ok, "a nested OneToOne has to write a single record; the type that arrived: %T", first[0]["price"])
	assert.Equal(t, 1990, price["amount"])
	assert.Nil(t, first[1]["price"], "the price of a variant with no link has to be nil")

	second, ok := got[1]["varyantlar"].([]query.Record)
	require.True(t, ok)
	require.Len(t, second, 1)
	price, ok = second[0]["price"].(query.Record)
	require.True(t, ok)
	assert.Equal(t, 2990, price["amount"])

	// Every level has to resolve within itself in a single call.
	assert.Equal(t, providerCalls{list: 1}, products.calls())
	assert.Equal(t, providerCalls{fetch: 1}, variants.calls())
	assert.Equal(t, providerCalls{fetch: 1}, prices.calls())
	assert.Equal(t, int64(2), links.listManyCalls.Load(), "one link round per expansion")
}

// --- no N+1 -----------------------------------------------------------------

func TestGraphDoesNoNPlusOne(t *testing.T) {
	const (
		rootCount    = 100
		variantCount = 2
	)

	links := newLinks(productVariant, variantPrice)
	productRecords := make([]query.Record, 0, rootCount)
	variantRecords := make([]query.Record, 0, rootCount*variantCount)
	priceRecords := make([]query.Record, 0, rootCount*variantCount)

	for i := range rootCount {
		productID := fmt.Sprintf("prod_%03d", i)
		productRecords = append(productRecords, query.Record{"id": productID})

		for j := range variantCount {
			variantID := fmt.Sprintf("var_%03d_%d", i, j)
			priceID := fmt.Sprintf("pset_%03d_%d", i, j)

			variantRecords = append(variantRecords, query.Record{"id": variantID})
			priceRecords = append(priceRecords, query.Record{"id": priceID, "amount": 100 * (i + 1)})

			links.connect("product_variant", productID, variantID)
			links.connect("variant_price", variantID, priceID)
		}
	}

	products := newProvider("product", productRecords...)
	variants := newProvider("variant", variantRecords...)
	prices := newProvider("pricing", priceRecords...)

	q := query.New(links, newContainer(t, products, variants, prices), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{
			Link:   "product_variant",
			Expand: []query.Expansion{{Link: "variant_price"}},
		}},
	})
	require.NoError(t, err)
	require.Len(t, got, rootCount)

	// The real claim: one call per expansion, INDEPENDENT of the record count.
	assert.Equal(t, providerCalls{list: 1}, products.calls(),
		"the root provider has to get a single List call")
	assert.Equal(t, providerCalls{fetch: 1}, variants.calls(),
		"the variant provider has to get a single FetchByIDs for %d root records", rootCount)
	assert.Equal(t, providerCalls{fetch: 1}, prices.calls(),
		"the pricing provider has to get a single FetchByIDs for %d variants", rootCount*variantCount)
	assert.Equal(t, int64(2), links.listManyCalls.Load(),
		"the links have to resolve in one round per expansion")
	assert.Zero(t, links.listCalls.Load(), "List must not be called per id")

	// The single call really has to carry EVERY id; otherwise the counter misleads.
	variantIDs, _ := variants.fetchArgs()
	assert.Len(t, variantIDs, rootCount*variantCount)
	priceIDs, _ := prices.fetchArgs()
	assert.Len(t, priceIDs, rootCount*variantCount)

	// The data has to be joined correctly too.
	last, ok := got[rootCount-1]["product_variant"].([]query.Record)
	require.True(t, ok)
	require.Len(t, last, variantCount)
	price, ok := last[0]["variant_price"].(query.Record)
	require.True(t, ok)
	assert.Equal(t, 100*rootCount, price["amount"])
}

// --- direction --------------------------------------------------------------

func TestGraphReverseDirectionWritesASingleRecordFromTheBulkResolve(t *testing.T) {
	variants := newProvider("variant",
		query.Record{"id": "var_1"},
		query.Record{"id": "var_3"},
	)
	products := newProvider("product",
		query.Record{"id": "prod_1", "title": "T-shirt"},
		query.Record{"id": "prod_2", "title": "Hat"},
	)

	links := newReverseLinks(productVariant)
	links.connect("product_variant", "prod_1", "var_1", "var_2")
	links.connect("product_variant", "prod_2", "var_3")

	q := query.New(links, newContainer(t, variants, products), nil)

	// The root entity sits at the link's TO end; it has to resolve in reverse.
	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "variant",
		Expand: []query.Expansion{{Link: "product_variant", As: "product"}},
	})
	require.NoError(t, err)
	require.Len(t, got, 2)

	first, ok := got[0]["product"].(query.Record)
	require.Truef(t, ok, "a reverse OneToMany has to write a single record; the type that arrived: %T", got[0]["product"])
	assert.Equal(t, "prod_1", first["id"])

	second, ok := got[1]["product"].(query.Record)
	require.True(t, ok)
	assert.Equal(t, "prod_2", second["id"])

	assert.Equal(t, int64(1), links.listManyByToCalls.Load(), "the reverse direction has to resolve in one round too")
	assert.Equal(t, providerCalls{fetch: 1}, products.calls())
}

func TestGraphReturnsInvalidWhenTheLinkDoesNotTouchTheRootEntity(t *testing.T) {
	orders := newProvider("order", query.Record{"id": "order_1"})
	q := query.New(newLinks(productVariant), newContainer(t, orders), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "order",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the expected class is Invalid, got: %v", err)
	assert.Contains(t, err.Error(), "order")
	assert.Contains(t, err.Error(), "product_variant")
}

// --- diagnosable errors -----------------------------------------------------

func TestGraphReturnsNotFoundAndNamesTheLookupWhenTheRootProviderIsMissing(t *testing.T) {
	q := query.New(newLinks(), container.New(nil), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product"})
	require.Error(t, err)
	assert.Nil(t, got)
	assert.True(t, errors.IsNotFound(err), "the expected class is NotFound, got: %v", err)

	// The name looked for has to be readable from query's OWN message rather than
	// from the container error UNDERNEATH (ADR 0004); hence the outermost typed error.
	var typed *errors.Error
	require.True(t, errors.As(err, &typed))
	assert.Contains(t, typed.Message, "product"+query.ProviderSuffix,
		"query's own message has to carry the name looked up in the container")
	assert.Equal(t, "product"+query.ProviderSuffix, typed.Details["looked_up_name"])
}

func TestGraphReturnsNotFoundAndNamesTheLookupWhenTheExpansionProviderIsMissing(t *testing.T) {
	variants := newProvider("variant", query.Record{"id": "var_1"})
	links := newLinks(variantPrice).connect("variant_price", "var_1", "pset_1")

	// pricing.query bilerek kaydedilmiyor.
	q := query.New(links, newContainer(t, variants), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "variant",
		Expand: []query.Expansion{{Link: "variant_price"}},
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "the expected class is NotFound, got: %v", err)

	var typed *errors.Error
	require.True(t, errors.As(err, &typed))
	assert.Contains(t, typed.Message, "pricing"+query.ProviderSuffix,
		"query's own message has to carry the name looked up in the container")
	assert.Equal(t, "pricing"+query.ProviderSuffix, typed.Details["looked_up_name"])
}

func TestGraphReturnsInvalidWhenTheProviderServesAnotherEntity(t *testing.T) {
	c := container.New(nil)
	// The provider under "product.query" serves ANOTHER entity; the mismatch is
	// the point of the test.
	require.NoError(t, c.Provide("product"+query.ProviderSuffix, newProvider("widget")))

	q := query.New(newLinks(), c, nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the expected class is Invalid, got: %v", err)
	assert.Contains(t, err.Error(), "product")
	assert.Contains(t, err.Error(), "product"+query.ProviderSuffix)
}

func TestGraphReturnsNotFoundForAnUnknownLinkName(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	q := query.New(newLinks(), newContainer(t, products), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "no_such_link"}},
	})
	require.Error(t, err)
	assert.Nil(t, got)
	assert.True(t, errors.IsNotFound(err), "the expected class is NotFound, got: %v", err)
	assert.Contains(t, err.Error(), "no_such_link")
}

func TestGraphARootProviderErrorDropsTheWholeCall(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	products.listErr = errors.Unavailable("product_down", "the product module is down")

	q := query.New(newLinks(), newContainer(t, products), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product"})
	require.Error(t, err)
	assert.Nil(t, got)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable),
		"the class of the underlying error has to be preserved, got: %v", err)
}

func TestGraphAnExpansionProviderErrorReturnsNoPartialResult(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1", "title": "T-shirt"})
	variants := newProvider("variant", query.Record{"id": "var_1"})
	variants.fetchErr = errors.Unavailable("variant_down", "the variant module is down")

	links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1")
	q := query.New(links, newContainer(t, products, variants), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err)
	assert.Nil(t, got, "no partial result may come back even though the root records were fetched")
	assert.True(t, errors.HasKind(err, errors.KindUnavailable),
		"the class of the underlying error has to be preserved, got: %v", err)
	assert.Contains(t, err.Error(), "variant")
}

func TestGraphALinkErrorDropsTheWholeCall(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	variants := newProvider("variant", query.Record{"id": "var_1"})

	links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1")
	links.listErr = errors.Unavailable("link_db_down", "the link table is unreachable")

	q := query.New(links, newContainer(t, products, variants), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "got: %v", err)
	assert.Zero(t, variants.calls().fetch, "with the link unresolved the provider must not be reached")
}

// --- the context ------------------------------------------------------------

func TestGraphReturnsEarlyUnderACanceledContext(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	q := query.New(newLinks(), newContainer(t, products), nil)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	got, err := q.Graph(ctx, query.GraphSpec{Entity: "product"})
	require.Error(t, err)
	assert.Nil(t, got)
	assert.ErrorIs(t, err, context.Canceled)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "got: %v", err)
	assert.Zero(t, products.calls().list, "under a canceled context the provider must not be reached at all")
}

func TestGraphWhenTheContextIsCanceledBeforeTheExpansion(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())

	products := newProvider("product", query.Record{"id": "prod_1"})
	variants := newProvider("variant", query.Record{"id": "var_1"})
	// The context is canceled right after the root listing finishes.
	products.afterList = cancel

	links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1")
	q := query.New(links, newContainer(t, products, variants), nil)

	_, err := q.Graph(ctx, query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	// Reading the definition is part of the PLANNING phase, and the plan
	// deliberately runs BEFORE the root List (so a broken spec errors without
	// paying for the root query). The definition may therefore have been read
	// once; what matters is that no link resolution and no expansion fetch
	// happens AFTER the cancellation.
	assert.Zero(t, links.listManyCalls.Load(), "the link service must not be reached after a cancellation")
	assert.Zero(t, variants.calls().fetch, "the expansion provider must not be reached after a cancellation")
}

// --- spec validation --------------------------------------------------------

func TestGraphRefusesAnInvalidSpec(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	q := query.New(newLinks(productVariant), newContainer(t, products), nil)

	tests := map[string]query.GraphSpec{
		"an empty entity": {Entity: ""},
		"negatif limit":   {Entity: "product", Limit: -1},
		"negatif offset":  {Entity: "product", Offset: -1},
		"an empty link name": {
			Entity: "product",
			Expand: []query.Expansion{{Link: ""}},
		},
		"a clashing key": {
			Entity: "product",
			Expand: []query.Expansion{
				{Link: "product_variant"},
				{Link: "product_channel", As: "product_variant"},
			},
		},
		"a nested empty link name": {
			Entity: "product",
			Expand: []query.Expansion{{
				Link:   "product_variant",
				Expand: []query.Expansion{{Link: ""}},
			}},
		},
	}

	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := q.Graph(t.Context(), spec)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "the expected class is Invalid, got: %v", err)
		})
	}

	assert.Zero(t, products.calls().list, "an invalid spec must not reach the provider at all")
}

func TestGraphRefusesAnOverlyDeepExpansion(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	q := query.New(newLinks(productVariant), newContainer(t, products), nil)

	// Twelve levels are built from the innermost expansion outwards.
	exp := query.Expansion{Link: "product_variant"}
	for i := range 11 {
		exp = query.Expansion{
			Link:   "product_variant",
			As:     fmt.Sprintf("seviye_%d", i),
			Expand: []query.Expansion{exp},
		}
	}

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{exp},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the expected class is Invalid, got: %v", err)
	assert.Zero(t, products.calls().list)
}

// --- a provider breaking the contract ---------------------------------------

func TestGraphCannotExpandARootRecordWithNoID(t *testing.T) {
	products := newProvider("product")
	products.order = []string{""}
	products.records = map[string]query.Record{"": {"title": "kimliksiz"}}

	links := newLinks(productVariant)
	q := query.New(links, newContainer(t, products, newProvider("variant")), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal), "got: %v", err)
	assert.Contains(t, err.Error(), query.IDField)
}

func TestGraphReturnsAnErrorForAnExpansionRecordWithNoID(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	variants := newProvider("variant")
	// The provider returns a record with no id; the join cannot be made.
	variants.order = []string{"var_1"}
	variants.records = map[string]query.Record{"var_1": {"sku": "TS-M"}}

	links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1")
	q := query.New(links, newContainer(t, products, variants), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal), "got: %v", err)
	assert.Contains(t, err.Error(), query.IDField)
}

// --- setup errors -----------------------------------------------------------

func TestGraphReturnsATypedErrorWithNoContainer(t *testing.T) {
	q := query.New(newLinks(), nil, nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product"})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal), "got: %v", err)
	assert.Contains(t, err.Error(), "product"+query.ProviderSuffix)
}

func TestGraphReturnsAnErrorOnExpansionWithNoLinkService(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	q := query.New(nil, newContainer(t, products), nil)

	// With no expansion the link service is never needed.
	got, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product"})
	require.NoError(t, err)
	assert.Len(t, got, 1)

	_, err = q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal), "got: %v", err)
}

// --- preserving the join key ------------------------------------------------

func TestGraphTheOutputKeyCannotOverwriteTheRootID(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1", "title": "T-shirt"})
	variants := newProvider("variant", query.Record{"id": "var_1"})

	links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1")
	q := query.New(links, newContainer(t, products, variants), nil)

	// Were As "id", the expansion result would be written OVER the root record's
	// id and the caller could no longer recognize the record; the later
	// expansions could not read the join key either.
	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant", As: query.IDField}},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the expected class is Invalid, got: %v", err)
	assert.Contains(t, err.Error(), query.IDField)
	assert.Zero(t, products.calls().list, "an invalid spec must not reach the provider at all")
}

func TestGraphANestedOutputKeyCannotOverwriteTheID(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	variants := newProvider("variant", query.Record{"id": "var_1"})
	prices := newProvider("pricing", query.Record{"id": "pset_1"})

	links := newLinks(productVariant, variantPrice)
	q := query.New(links, newContainer(t, products, variants, prices), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{
			Link:   "product_variant",
			Expand: []query.Expansion{{Link: "variant_price", As: query.IDField}},
		}},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the expected class is Invalid, got: %v", err)
	assert.Zero(t, products.calls().list)
}

func TestGraphARootRecordWithAnUnreadableIDIsNotSkippedSilently(t *testing.T) {
	products := newProvider("product")
	products.order = []string{"prod_1", "kimliksiz"}
	products.records = map[string]query.Record{
		"prod_1":    {"id": "prod_1", "title": "kimlikli"},
		"kimliksiz": {"title": "kimliksiz"},
	}
	variants := newProvider("variant", query.Record{"id": "var_1"})

	links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1")
	q := query.New(links, newContainer(t, products, variants), nil)

	// A skipped record NEVER gets the expansion key; the result slice stays
	// heterogeneous and missing data looks like a correct result. The policy is
	// to return no partial result.
	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err, "a root record whose id cannot be read must not be silently left out of the expansion")
	assert.Nil(t, got, "no partial result may come back")
	assert.True(t, errors.HasKind(err, errors.KindInternal), "got: %v", err)
	assert.Contains(t, err.Error(), query.IDField)
}

func TestGraphNamesTheTypeWhenTheIDFieldIsNotAString(t *testing.T) {
	products := newProvider("product")
	products.order = []string{"uuid"}
	// This is how a uuid column arrives from a provider fed by pgx.RowToMap.
	products.records = map[string]query.Record{"uuid": {"id": [16]byte{1}, "title": "a uuid identity"}}
	variants := newProvider("variant", query.Record{"id": "var_1"})

	q := query.New(newLinks(productVariant), newContainer(t, products, variants), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("%T", [16]byte{}),
		"with the field PRESENT but of the wrong type the message has to name the type that arrived; otherwise the error blames the wrong side")
}

// --- record ownership -------------------------------------------------------

func TestGraphDoesNotCorruptTheProvidersRecords(t *testing.T) {
	products := newSharingProvider("product", query.Record{"id": "prod_1", "title": "T-shirt"})
	variants := newSharingProvider("variant", query.Record{"id": "var_1", "sku": "TS-M"})
	prices := newSharingProvider("pricing", query.Record{"id": "pset_1", "amount": 1990})

	c := container.New(nil)
	for _, p := range []*sharingProvider{products, variants, prices} {
		require.NoError(t, c.Provide(p.Entity()+query.ProviderSuffix, p))
	}

	links := newLinks(productVariant, variantPrice)
	links.connect("product_variant", "prod_1", "var_1")
	links.connect("variant_price", "var_1", "pset_1")

	q := query.New(links, c, nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{
			Link:   "product_variant",
			Expand: []query.Expansion{{Link: "variant_price"}},
		}},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Contains(t, got[0], "product_variant", "the expansion really has to have happened")

	// The provider does not copy its records; Query has to write into its own copy
	// so the module's state is not corrupted (a stale field leaking and a data
	// race on concurrent calls are born here).
	assert.Equal(t, query.Record{"id": "prod_1", "title": "T-shirt"}, products.records[0],
		"the expansion key must not be written into the root provider's record")
	assert.Equal(t, query.Record{"id": "var_1", "sku": "TS-M"}, variants.records[0],
		"the nested expansion key must not be written into the expansion provider's record")
	assert.Equal(t, query.Record{"id": "pset_1", "amount": 1990}, prices.records[0])
}

// --- cancellation classification --------------------------------------------

func TestGraphReturnsUnavailableForARawContextError(t *testing.T) {
	// A provider and a link service may return an UNTYPED context error (that is
	// what pgx returns directly). If that error falls to KindInternal, the API
	// boundary produces a 500 with a masked message instead of a 503.
	t.Run("kok list", func(t *testing.T) {
		products := newProvider("product", query.Record{"id": "prod_1"})
		products.listErr = context.DeadlineExceeded
		q := query.New(newLinks(), newContainer(t, products), nil)

		_, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product"})
		require.Error(t, err)
		assert.True(t, errors.HasKind(err, errors.KindUnavailable),
			"the expected class is Unavailable, got: %v (%v)", errors.KindOf(err), err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("genisletme fetch", func(t *testing.T) {
		products := newProvider("product", query.Record{"id": "prod_1"})
		variants := newProvider("variant", query.Record{"id": "var_1"})
		variants.fetchErr = context.Canceled

		links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1")
		q := query.New(links, newContainer(t, products, variants), nil)

		_, err := q.Graph(t.Context(), query.GraphSpec{
			Entity: "product",
			Expand: []query.Expansion{{Link: "product_variant"}},
		})
		require.Error(t, err)
		assert.True(t, errors.HasKind(err, errors.KindUnavailable),
			"the expected class is Unavailable, got: %v (%v)", errors.KindOf(err), err)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("link servisi", func(t *testing.T) {
		products := newProvider("product", query.Record{"id": "prod_1"})
		variants := newProvider("variant", query.Record{"id": "var_1"})

		links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1")
		links.listErr = context.DeadlineExceeded
		q := query.New(links, newContainer(t, products, variants), nil)

		_, err := q.Graph(t.Context(), query.GraphSpec{
			Entity: "product",
			Expand: []query.Expansion{{Link: "product_variant"}},
		})
		require.Error(t, err)
		assert.True(t, errors.HasKind(err, errors.KindUnavailable),
			"the expected class is Unavailable, got: %v (%v)", errors.KindOf(err), err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("link tanimi", func(t *testing.T) {
		products := newProvider("product", query.Record{"id": "prod_1"})
		variants := newProvider("variant", query.Record{"id": "var_1"})

		links := newLinks(productVariant)
		links.defErr = context.Canceled
		q := query.New(links, newContainer(t, products, variants), nil)

		_, err := q.Graph(t.Context(), query.GraphSpec{
			Entity: "product",
			Expand: []query.Expansion{{Link: "product_variant"}},
		})
		require.Error(t, err)
		assert.True(t, errors.HasKind(err, errors.KindUnavailable),
			"the expected class is Unavailable, got: %v (%v)", errors.KindOf(err), err)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("a typed error class is preserved", func(t *testing.T) {
		// The cancellation split must not overwrite the class a provider gave DELIBERATELY.
		products := newProvider("product", query.Record{"id": "prod_1"})
		products.listErr = errors.Invalid("product_bad_filter", "unknown filter")
		q := query.New(newLinks(), newContainer(t, products), nil)

		_, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product"})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err), "got: %v", err)
	})
}

// --- validation independent of the data -------------------------------------

func TestGraphValidatesANestedExpansionEvenWithNoData(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	variants := newProvider("variant", query.Record{"id": "var_1"})
	channels := newProvider("channel", query.Record{"id": "sc_web"})

	// There is no link at all: the top-level expansion produces an empty slice.
	// The product_channel link is between product and channel, it does NOT link to variant.
	links := newLinks(productVariant, productChannel)
	q := query.New(links, newContainer(t, products, variants, channels), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{
			Link:   "product_variant",
			Expand: []query.Expansion{{Link: "product_channel"}},
		}},
	})
	require.Error(t, err, "a spec error at a lower level has to be reported even when the level above fetches no data")
	assert.True(t, errors.IsInvalid(err), "the expected class is Invalid, got: %v", err)
	assert.Contains(t, err.Error(), "product_channel")
	assert.Contains(t, err.Error(), "variant")
}

func TestGraphValidatesTheTargetProviderRegistrationEvenWithNoData(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})

	// variant.query is deliberately not registered and there is no link at all:
	// in the old behavior a forgotten registration stayed silent while the data was empty.
	links := newLinks(productVariant)
	q := query.New(links, newContainer(t, products), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err, "a target provider that is not registered has to be reported even with no data")
	assert.True(t, errors.IsNotFound(err), "the expected class is NotFound, got: %v", err)

	var typed *errors.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, "variant"+query.ProviderSuffix, typed.Details["looked_up_name"])
}

func TestGraphValidatesTheExpansionTreeEvenWithNoRootRecord(t *testing.T) {
	products := newProvider("product")
	q := query.New(newLinks(productVariant), newContainer(t, products, newProvider("variant")), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "no_such_link"}},
	})
	require.Error(t, err, "a broken expansion definition has to be reported even with no root record")
	assert.Nil(t, got)
	assert.True(t, errors.IsNotFound(err), "the expected class is NotFound, got: %v", err)
	assert.Contains(t, err.Error(), "no_such_link")
}

// --- the width limit --------------------------------------------------------

func TestGraphRefusesAnOverlyWideExpansion(t *testing.T) {
	// A spec staying UNDER the depth limit but carrying many expansions opens
	// hundreds of round trips in one request too; the cost grows with the number
	// of expansions rather than with the depth.
	t.Run("tek seviye", func(t *testing.T) {
		products := newProvider("product", query.Record{"id": "prod_1"})
		q := query.New(newLinks(productVariant), newContainer(t, products), nil)

		exps := make([]query.Expansion, 0, 51)
		for i := range 51 {
			exps = append(exps, query.Expansion{Link: "product_variant", As: fmt.Sprintf("v_%d", i)})
		}

		_, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product", Expand: exps})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err), "the expected class is Invalid, got: %v", err)
		assert.Zero(t, products.calls().list, "an invalid spec must not reach the provider at all")
	})

	t.Run("ic ice", func(t *testing.T) {
		products := newProvider("product", query.Record{"id": "prod_1"})
		q := query.New(newLinks(productVariant), newContainer(t, products), nil)

		// 3 levels, 4 siblings at each: 4 + 16 + 64 = 84 expansions.
		// The depth limit (10) does not stop this spec.
		var derinlestir func(kalan int) []query.Expansion
		derinlestir = func(kalan int) []query.Expansion {
			if kalan == 0 {
				return nil
			}
			out := make([]query.Expansion, 0, 4)
			for i := range 4 {
				out = append(out, query.Expansion{
					Link:   "product_variant",
					As:     fmt.Sprintf("s%d_%d", kalan, i),
					Expand: derinlestir(kalan - 1),
				})
			}
			return out
		}

		_, err := q.Graph(t.Context(), query.GraphSpec{
			Entity: "product",
			Expand: derinlestir(3),
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err), "the expected class is Invalid, got: %v", err)
		assert.Zero(t, products.calls().list)
	})
}

// TestGraphABrokenSpecDoesNotChargeForTheRootQuery verifies that the expansion
// plan is resolved BEFORE the root data is fetched.
//
// Regression: the plan ran AFTER the root List call. That had two consequences:
// (1) a query carrying an unknown link name paid for a full root query before
// erroring; (2) more seriously, a transient error from the provider MASKED the
// deterministic spec error — a "the database is unreachable" error hid what was
// really a typo waiting to be fixed.
func TestGraphABrokenSpecDoesNotChargeForTheRootQuery(t *testing.T) {
	t.Run("an unknown link is refused before the root query runs", func(t *testing.T) {
		products := newProvider("product", query.Record{"id": "prod_1"})
		links := newLinks(productVariant)
		q := query.New(links, newContainer(t, products), nil)

		_, err := q.Graph(t.Context(), query.GraphSpec{
			Entity: "product",
			Expand: []query.Expansion{{Link: "no_such_link"}},
		})
		require.Error(t, err)
		assert.Zero(t, products.calls().list,
			"a broken spec must not charge for the root query")
	})

	t.Run("a provider error does not mask a spec error", func(t *testing.T) {
		products := newProvider("product", query.Record{"id": "prod_1"})
		// The root provider is broken too: this error used to come first and hide
		// the link error.
		products.listErr = errors.Unavailable("db_down", "the database is unreachable")

		links := newLinks(productVariant)
		q := query.New(links, newContainer(t, products), nil)

		_, err := q.Graph(t.Context(), query.GraphSpec{
			Entity: "product",
			Expand: []query.Expansion{{Link: "no_such_link"}},
		})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "db_down",
			"a deterministic spec error must not hide behind a transient provider error")
		assert.Contains(t, err.Error(), "no_such_link",
			"the error has to name which link was not found")
	})
}
