//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
	pricingmodels "github.com/bdrtr/gobit/internal/modules/pricing/models"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file proves the three storefront catalog filters against the REAL stack:
// ADR 0039's option value, ADR 0040's availability and ADR 0041's price.
//
// # Why it has to go through HTTP with the real modules
//
// Two of the three cannot be proved anywhere else, and the reason is the same
// one ADR 0040 spends a paragraph on. "In stock" is the CATALOG's answer over an
// INVENTORY fact: the catalog holds manage_inventory and allow_backorder, and it
// reads the quantity out of a loosely typed record inventory publishes under the
// name "available_quantity". The catalog cannot import that constant
// (Principle 2.1), so the two sides of the contract are two string literals in
// two modules with nothing between them.
//
// Every test that hands the catalog a HAND-WRITTEN record agrees with itself by
// construction: it spells the name on both sides of its own fixture. Only a run
// with the real inventory module filling the record proves the two literals
// still name one field -- and the failure that proves matters, because a rename
// makes every counted variant read as "no quantity", the badge answers "out of
// stock" for the whole catalog, and the response is a perfectly ordinary 200.
// internal/arch guards the pairing statically; this is the same claim measured
// rather than parsed.
//
// The price filter has the same shape against pricing, one layer worse: pricing
// publishes its price sub-record names as UNEXPORTED constants, so no static
// audit can bind them at all (see internal/arch/provider_fields_test.go,
// catalogUnboundForeignFields). Here the amounts come out of pricing itself.
//
// The option-value filter needs none of that -- it is a WHERE clause over
// product's own tables -- and it is here anyway, because what a shopper actually
// does is click a value in the vocabulary endpoint and send it back, and only a
// round trip shows the two ends agreeing on the spelling.
//
// # Why the ground is isolated with a collection
//
// The shared package fixture holds dozens of products with no channel assignment
// and no stock, so an unnarrowed listing would drown these four. Every request
// below carries collection_id, which is also what lets the assertions name exact
// sets rather than "contains".

// The fixture constants of the catalog-filter ground.
const (
	// catalogFilterCollectionHandle separates these products from the shared
	// catalog.
	catalogFilterCollectionHandle = "e2e-catalog-filter"
	// catalogFilterCurrency is the currency every fixture price is written in.
	// A second currency appears only where a test is about the currency itself.
	catalogFilterCurrency = "TRY"
)

// catalogFilterRed is one Turkish color name written without a Turkish letter
// in this file (ADR 0012).
//
// Spelled out: "K", the DOTLESS i (U+0131), "rm", the dotless i again, "z" and
// the dotless i once more. One fixture product carries this spelling and another
// carries the plain ASCII "KIRMIZI"; they are two spellings of one word, they
// fold to one form, and a shopper who types either has to find both. That is the
// whole of ADR 0039.
const catalogFilterRed = "K\u0131rm\u0131z\u0131"

// catalogFilterProduct is one fixture product and what the tests expect of it.
type catalogFilterProduct struct {
	id     string
	handle string
}

// catalogFilter is the set-up ground of the filter scenario.
//
// The four products differ in exactly the dimensions the three filters read, and
// in nothing else: all four are published, in one collection, assigned to no
// channel and therefore visible in every storefront.
type catalogFilter struct {
	collectionID string

	// stocked is counted, linked to an inventory item holding five units, and
	// priced at 10000. It offers the color in its Turkish spelling.
	stocked catalogFilterProduct
	// soldOut is counted and linked, and its item holds ZERO units. Same price,
	// and it offers a different color.
	soldOut catalogFilterProduct
	// unlinked is counted and linked to NOTHING, which ADR 0040 answers "out of
	// stock" for: a variant that says it is counted while nothing counts it has
	// no evidence of stock. It is priced at 50000 and offers the color in the
	// plain ASCII spelling.
	unlinked catalogFilterProduct
	// unmanaged is NOT counted, so it is in stock by the first clause alone
	// even though it has no inventory item either. It carries the fixture's
	// only price that belongs to a price LIST, so ADR 0041's base-price rule
	// leaves it out of every bracket.
	unmanaged catalogFilterProduct
}

// The state that keeps the ground set up once.
var (
	catalogFilterOnce   sync.Once
	catalogFilterGround catalogFilter
	catalogFilterErr    error
)

// catalogFilterFixture sets the ground up once and hands it to the tests.
//
// The handles and the collection handle are fixed rather than counted, so a
// failure names a record a reader can find; setting them up twice would collide
// on the unique handle, which is why the body sits inside a [sync.Once] (the
// same pattern as [channelCatalogFixture]).
func catalogFilterFixture(t *testing.T) catalogFilter {
	t.Helper()

	catalogFilterOnce.Do(func() {
		// Not t.Context(): the ground outlives the first test that asks for it,
		// and that test's context is cancelled when it ends.
		catalogFilterGround, catalogFilterErr = setUpCatalogFilter(context.Background())
	})
	require.NoError(t, catalogFilterErr, "the catalog filter fixture could not be set up")

	return catalogFilterGround
}

// setUpCatalogFilter writes the four products described on [catalogFilter].
func setUpCatalogFilter(ctx context.Context) (catalogFilter, error) {
	var ground catalogFilter

	collection, err := productSvc.CreateCollection(ctx, productsvc.CreateCollectionInput{
		Title:  "E2E Catalog Filter",
		Handle: catalogFilterCollectionHandle,
	})
	if err != nil {
		return ground, fmt.Errorf("the isolation collection could not be set up: %w", err)
	}
	ground.collectionID = collection.ID

	if ground.stocked, err = setUpFilterProduct(ctx, filterProductSpec{
		collectionID: collection.ID,
		name:         "stocked",
		color:        catalogFilterRed,
		amount:       10000,
		manage:       true,
		stock:        ptrOf(int64(5)),
	}); err != nil {
		return ground, err
	}

	if ground.soldOut, err = setUpFilterProduct(ctx, filterProductSpec{
		collectionID: collection.ID,
		name:         "sold-out",
		color:        "Mavi",
		amount:       10000,
		manage:       true,
		stock:        ptrOf(int64(0)),
	}); err != nil {
		return ground, err
	}

	if ground.unlinked, err = setUpFilterProduct(ctx, filterProductSpec{
		collectionID: collection.ID,
		name:         "unlinked",
		// The SAME color as "stocked", spelled without the Turkish letters.
		color:  "KIRMIZI",
		amount: 50000,
		manage: true,
		// No stock pointer at all: no inventory item and no link.
		stock: nil,
	}); err != nil {
		return ground, err
	}

	if ground.unmanaged, err = setUpFilterProduct(ctx, filterProductSpec{
		collectionID: collection.ID,
		name:         "unmanaged",
		color:        "Yesil",
		amount:       90000,
		manage:       false,
		stock:        nil,
		onPriceList:  true,
	}); err != nil {
		return ground, err
	}

	return ground, nil
}

// filterProductSpec describes one fixture product.
type filterProductSpec struct {
	collectionID string
	name         string
	color        string
	amount       int64
	// manage is the variant's manage_inventory flag.
	manage bool
	// stock, when non-nil, means an inventory item is created, LINKED to the
	// variant and leveled at the shared location with that quantity. nil means
	// no item and no link, which is a different state from a level of zero and
	// the one ADR 0040 answers false for on a counted variant.
	stock *int64
	// onPriceList puts the price on a price LIST instead of writing it as the
	// base price, which is what makes ADR 0041 skip it.
	onPriceList bool
}

// setUpFilterProduct writes one product with its option value, its price and,
// where the spec asks for it, its stock.
func setUpFilterProduct(ctx context.Context, spec filterProductSpec) (catalogFilterProduct, error) {
	var out catalogFilterProduct

	handle := "e2e-catalog-filter-" + spec.name
	manage := spec.manage

	product, err := productSvc.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle:       handle,
		Title:        "E2E Catalog Filter " + spec.name,
		Status:       productmodels.StatusPublished,
		CollectionID: &spec.collectionID,
		Options:      []productsvc.CreateOptionInput{{Title: "Color", Values: []string{spec.color}}},
		Variants: []productsvc.CreateVariantInput{{
			Title:           spec.name,
			ManageInventory: &manage,
			Options:         map[string]string{"Color": spec.color},
		}},
	})
	if err != nil {
		return out, fmt.Errorf("the %q product could not be set up: %w", handle, err)
	}
	if len(product.Variants) != 1 {
		return out, fmt.Errorf("the %q product came back with %d variants", handle, len(product.Variants))
	}
	variantID := product.Variants[0].ID

	if err := setUpFilterPrice(ctx, variantID, spec); err != nil {
		return out, err
	}
	if err := setUpFilterStock(ctx, variantID, spec); err != nil {
		return out, err
	}

	return catalogFilterProduct{id: product.ID, handle: product.Handle}, nil
}

// setUpFilterPrice writes the variant's price, on a list or as the base price.
//
// The list case is what makes ADR 0041's second point observable: pricing hands
// the catalog the price either way, and only "price_list_id IS NULL" tells the
// two apart. A fixture where every price were a base price could not fail on a
// build that ignored the list.
func setUpFilterPrice(ctx context.Context, variantID string, spec filterProductSpec) error {
	set, err := pricingSvc.CreatePriceSet(ctx, nil)
	if err != nil {
		return fmt.Errorf("the %q price set could not be set up: %w", spec.name, err)
	}
	if err := productSvc.SetVariantPriceSet(ctx, variantID, set.ID); err != nil {
		return fmt.Errorf("the %q price set could not be linked: %w", spec.name, err)
	}

	input := pricingsvc.PriceInput{
		CurrencyCode: catalogFilterCurrency,
		Amount:       spec.amount,
		MinQuantity:  1,
	}

	if spec.onPriceList {
		list, err := pricingSvc.CreatePriceList(ctx, pricingsvc.PriceListInput{
			Title: "E2E Catalog Filter " + spec.name,
			Type:  pricingmodels.PriceListSale,
			// Active rather than draft: pricing drops the prices of an unusable
			// list before it answers the Query layer at all, so a draft list
			// would make this product carry NO price and the test would pass
			// for the wrong reason -- it would prove nothing about
			// "price_list_id IS NULL".
			Status: pricingmodels.PriceListActive,
		})
		if err != nil {
			return fmt.Errorf("the %q price list could not be set up: %w", spec.name, err)
		}
		input.PriceListID = &list.ID
	}

	if _, err := pricingSvc.SetPrices(ctx, set.ID, []pricingsvc.PriceInput{input}); err != nil {
		return fmt.Errorf("the %q price could not be written: %w", spec.name, err)
	}

	return nil
}

// setUpFilterStock creates and links the inventory item when the spec asks for
// one.
func setUpFilterStock(ctx context.Context, variantID string, spec filterProductSpec) error {
	if spec.stock == nil {
		return nil
	}

	item, err := inventorySvc.CreateInventoryItem(ctx, inventorysvc.CreateInventoryItemInput{
		SKU:   "E2E-FILTER-" + spec.name,
		Title: spec.name,
	})
	if err != nil {
		return fmt.Errorf("the %q inventory item could not be set up: %w", spec.name, err)
	}
	if err := productSvc.SetVariantInventoryItem(ctx, variantID, item.ID); err != nil {
		return fmt.Errorf("the %q inventory link could not be made: %w", spec.name, err)
	}
	if _, err := inventorySvc.SetInventoryLevel(ctx, item.ID, stockLocationID, *spec.stock); err != nil {
		return fmt.Errorf("the %q stock level could not be written: %w", spec.name, err)
	}

	return nil
}

// ptrOf returns the address of a value.
func ptrOf[T any](v T) *T { return &v }

// filteredCatalog is the storefront envelope with the badge decoded.
//
// It is a type of its own rather than a field on [storefrontEnvelope] because
// what it adds is the subject of ADR 0040: the flag on the PRODUCT and the flag
// on each VARIANT, which a client renders as two different things.
type filteredCatalog struct {
	Data []struct {
		ID       string `json:"id"`
		Handle   string `json:"handle"`
		InStock  bool   `json:"in_stock"`
		Variants []struct {
			InStock bool `json:"in_stock"`
		} `json:"variants"`
	} `json:"data"`
	Count      *int   `json:"count"`
	NextCursor string `json:"next_cursor"`
}

// ids returns the product identities in the envelope.
func (c filteredCatalog) ids() []string {
	out := make([]string, 0, len(c.Data))
	for _, product := range c.Data {
		out = append(out, product.ID)
	}

	return out
}

// filteredCatalogRequest calls the storefront listing with the fixture's
// collection narrowing plus whatever the case adds, and returns the recorder.
//
// The narrowing is applied HERE rather than at each call site so that no case
// can forget it: a request that lost it would answer over the whole shared
// catalog and its assertion would turn into "contains", which is exactly the
// shape that stops failing.
func filteredCatalogRequest(t *testing.T, ground catalogFilter, query url.Values) *http.Response {
	t.Helper()

	recorder := magazaIstegi(t,
		catalogPath(testChannelID, "/products")+"?"+
			mergedFilterQuery(ground, query).Encode(), publishableKey)

	return recorder.Result()
}

// filteredCatalogOf runs the listing and requires a 200.
func filteredCatalogOf(t *testing.T, ground catalogFilter, query url.Values) filteredCatalog {
	t.Helper()

	recorder := magazaIstegi(t,
		catalogPath(testChannelID, "/products")+"?"+
			mergedFilterQuery(ground, query).Encode(), publishableKey)
	require.Equal(t, http.StatusOK, recorder.Code,
		"the storefront listing must answer 200; body: %s", recorder.Body.String())

	var envelope filteredCatalog
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope),
		"the storefront listing could not be decoded; body: %s", recorder.Body.String())

	return envelope
}

// mergedFilterQuery adds the fixture's collection narrowing to a case's query.
func mergedFilterQuery(ground catalogFilter, query url.Values) url.Values {
	full := url.Values{"collection_id": {ground.collectionID}}
	for key, values := range query {
		full[key] = slices.Clone(values)
	}

	return full
}

// TestTheStorefrontAnswersInStockOverTheRealInventoryModule is the end-to-end
// proof of ADR 0040, badge and filter.
//
// Every input meets here for the first time in production wiring: the two flags
// come out of product's own columns, the quantity comes out of inventory's
// ledger through the Query layer, and the name that carries it is spelled once
// in each module with nothing but this test and an arch audit comparing them.
func TestTheStorefrontAnswersInStockOverTheRealInventoryModule(t *testing.T) {
	ground := catalogFilterFixture(t)

	whole := filteredCatalogOf(t, ground, nil)
	require.Len(t, whole.Data, 4, "the fixture collection holds exactly four products")

	badges := map[string]bool{}
	for _, product := range whole.Data {
		badges[product.ID] = product.InStock
		require.Len(t, product.Variants, 1)
		assert.Equal(t, product.InStock, product.Variants[0].InStock,
			"a one-variant product must carry its variant's answer")
	}

	assert.True(t, badges[ground.stocked.id], "five units at the location is in stock")
	assert.False(t, badges[ground.soldOut.id], "a counted variant at zero is not")
	assert.False(t, badges[ground.unlinked.id],
		"a counted variant that nothing counts has no evidence of stock; answering "+
			"otherwise would sell what the shop may not have")
	assert.True(t, badges[ground.unmanaged.id],
		"manage_inventory false is sellable on its own, with no inventory item at all")

	inStock := filteredCatalogOf(t, ground, url.Values{"in_stock": {"true"}})
	assert.ElementsMatch(t, []string{ground.stocked.id, ground.unmanaged.id}, inStock.ids(),
		"the filter must select exactly what the badge reports")

	outOfStock := filteredCatalogOf(t, ground, url.Values{"in_stock": {"false"}})
	assert.ElementsMatch(t, []string{ground.soldOut.id, ground.unlinked.id}, outOfStock.ids(),
		"the other direction is a filter of its own, not the absence of one")

	assert.Nil(t, inStock.Count,
		"a filtered listing carries no count: the total would have to be computed over "+
			"the whole catalog")
}

// TestTheStorefrontMatchesAnOptionValueAcrossItsSpellings is the end-to-end
// proof of ADR 0039.
//
// Two products offer one color under two spellings, and one request has to find
// both. The value is also fetched from the VOCABULARY endpoint first, because
// that is what a storefront does -- it has the word a shopper clicked, and a
// value the vocabulary hands back that the filter cannot find would be a
// capability with no caller.
func TestTheStorefrontMatchesAnOptionValueAcrossItsSpellings(t *testing.T) {
	ground := catalogFilterFixture(t)

	recorder := magazaIstegi(t, catalogPath(testChannelID, "/option-values"), publishableKey)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var vocabulary struct {
		Data []struct {
			OptionTitle string `json:"option_title"`
			Value       string `json:"value"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &vocabulary),
		"the option vocabulary could not be decoded; body: %s", recorder.Body.String())

	offered := make([]string, 0, len(vocabulary.Data))
	for _, pair := range vocabulary.Data {
		offered = append(offered, pair.Value)
	}
	require.Contains(t, offered, catalogFilterRed,
		"the vocabulary must hand back the value AS THE MERCHANT TYPED IT")

	// The value the vocabulary just offered, sent straight back.
	verbatim := filteredCatalogOf(t, ground, url.Values{"option_value": {catalogFilterRed}})
	assert.ElementsMatch(t, []string{ground.stocked.id, ground.unlinked.id}, verbatim.ids(),
		"the two spellings of one color must meet")

	// And the same request typed by a shopper with an English keyboard.
	folded := filteredCatalogOf(t, ground, url.Values{"option_value": {"kirmizi"}})
	assert.Equal(t, verbatim.ids(), folded.ids(),
		"the match is on the folded form, so the spelling of the REQUEST does not matter")

	other := filteredCatalogOf(t, ground, url.Values{"option_value": {"mavi"}})
	assert.Equal(t, []string{ground.soldOut.id}, other.ids(),
		"a different value must not be merged into the first")

	assert.Equal(t, 1, derefCount(t, other),
		"the option value is a WHERE clause, so the count describes the filtered set")
}

// TestTheStorefrontFiltersByTheBasePriceOverTheRealPricingModule is the
// end-to-end proof of ADR 0041.
//
// The amounts come out of pricing itself, which is the only way to see the
// decision's second point work: "unmanaged" is priced at 90000 on a price LIST
// and at nothing else, so a build that compared every price it was handed --
// rather than only the one with no list -- would put it inside a wide bracket
// and this test would fail.
func TestTheStorefrontFiltersByTheBasePriceOverTheRealPricingModule(t *testing.T) {
	ground := catalogFilterFixture(t)

	cheap := filteredCatalogOf(t, ground, url.Values{
		"currency_code": {catalogFilterCurrency},
		"max_price":     {"20000"},
	})
	assert.ElementsMatch(t, []string{ground.stocked.id, ground.soldOut.id}, cheap.ids(),
		"only the two products priced at 10000 are under the bound")

	everything := filteredCatalogOf(t, ground, url.Values{
		"currency_code": {catalogFilterCurrency},
		"min_price":     {"0"},
		"max_price":     {"1000000"},
	})
	assert.ElementsMatch(t,
		[]string{ground.stocked.id, ground.soldOut.id, ground.unlinked.id}, everything.ids(),
		"a bracket wide enough for every amount still leaves out the variant that is "+
			"priced ONLY on a list: it has no base price to compare")

	foreign := filteredCatalogOf(t, ground, url.Values{
		"currency_code": {"USD"},
		"max_price":     {"1000000"},
	})
	assert.Empty(t, foreign.ids(),
		"the fixture holds no price in that currency and nothing is converted")
}

// TestTheTwoEnrichedFiltersNarrowTogetherOverHTTP pins that the three filters
// are one surface rather than three endpoints.
func TestTheTwoEnrichedFiltersNarrowTogetherOverHTTP(t *testing.T) {
	ground := catalogFilterFixture(t)

	both := filteredCatalogOf(t, ground, url.Values{
		"in_stock":      {"true"},
		"currency_code": {catalogFilterCurrency},
		"max_price":     {"20000"},
	})
	assert.Equal(t, []string{ground.stocked.id}, both.ids(),
		"only the cheap product that can be bought satisfies both")

	withValue := filteredCatalogOf(t, ground, url.Values{
		"in_stock":     {"false"},
		"option_value": {"kirmizi"},
	})
	assert.Equal(t, []string{ground.unlinked.id}, withValue.ids(),
		"a WHERE-clause filter and an enriched one narrow together")
}

// TestAnEnrichedFilterRefusesTheCounterAndTheOffset proves the two refusals the
// endpoint documents actually reach a client as 422.
//
// Both are combinations a build could easily have answered instead: the count of
// the unfiltered set is a number that describes a different set, and an offset
// counts catalog rows rather than matches, so the second page would begin
// somewhere the first had already shown. Neither would raise anything at run
// time, which is why the refusal is worth a test at the edge.
func TestAnEnrichedFilterRefusesTheCounterAndTheOffset(t *testing.T) {
	ground := catalogFilterFixture(t)

	refused := map[string]url.Values{
		"the counter beside the stock filter": {"in_stock": {"true"}, "with_count": {"true"}},
		"an offset beside the stock filter":   {"in_stock": {"true"}, "offset": {"1"}},
		"an offset beside the price filter": {
			"currency_code": {catalogFilterCurrency}, "max_price": {"1"}, "offset": {"1"},
		},
		"a price bound with no currency": {"max_price": {"1"}},
		"a currency with no bound":       {"currency_code": {catalogFilterCurrency}},
		"a reversed pair": {
			"currency_code": {catalogFilterCurrency}, "min_price": {"5"}, "max_price": {"1"},
		},
	}

	for name, query := range refused {
		t.Run(name, func(t *testing.T) {
			response := filteredCatalogRequest(t, ground, query)
			defer func() { _ = response.Body.Close() }()

			assert.Equal(t, http.StatusUnprocessableEntity, response.StatusCode,
				"the request has to be refused rather than answered with something else")
		})
	}
}

// TestPagingAFilteredCatalogOverHTTPVisitsEachProductOnce walks the filtered
// catalog one product at a time.
//
// It is the claim the scan exists for, made where it can actually fail: a resume
// point taken from the wrong row loses the matches a chunk still held or returns
// one twice, and both look like a working filter until somebody counts. Paging
// by cursor is also the ONLY way to page here, because an offset is refused.
func TestPagingAFilteredCatalogOverHTTPVisitsEachProductOnce(t *testing.T) {
	ground := catalogFilterFixture(t)

	whole := filteredCatalogOf(t, ground, url.Values{"in_stock": {"false"}})
	require.Len(t, whole.Data, 2)

	var (
		walked []string
		cursor string
	)
	for range 5 {
		query := url.Values{"in_stock": {"false"}, "limit": {"1"}}
		if cursor != "" {
			query.Set("after", cursor)
		}

		page := filteredCatalogOf(t, ground, query)
		walked = append(walked, page.ids()...)

		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}

	assert.Empty(t, cursor, "the walk must reach the end of the catalog")
	assert.Equal(t, whole.ids(), walked,
		"paging one at a time must produce exactly what one request produces")
}

// derefCount reads the envelope's counter and fails when it was not counted.
//
// A raw dereference would conflate two claims -- that the number is right and
// that it was COMPUTED -- and would panic instead of failing readably the day
// somebody widened the "not counted" rule to a listing that can be counted.
func derefCount(t *testing.T, catalog filteredCatalog) int {
	t.Helper()

	require.NotNil(t, catalog.Count, "this listing must carry a count")

	return *catalog.Count
}
