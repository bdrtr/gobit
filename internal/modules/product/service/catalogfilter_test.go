package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file covers the three catalog filters ADR 0039, 0040 and 0041 decide,
// and the badge ADR 0040 owes beside its filter.
//
// The three are tested together because they are one surface: they share the
// paging machinery, and two of them share the road that reads another module's
// loosely typed record. What is NOT shared is where the answer is computed, and
// most of the assertions below are about that difference -- the option value is
// a predicate the database applies, so paging and counting are the database's;
// the other two are computed after the rows are read, so the listing scans.

// --- ADR 0040: the badge ------------------------------------------------

// TestTheInStockBadgeFollowsTheDefinition walks every clause of ADR 0040's
// variant rule, including the two that answer FALSE.
//
// The table is the decision written out. A test per clause would pass while a
// second clause silently stopped being consulted, because each clause alone is
// enough to make the answer true; only the negative rows pin the difference
// between "the rule ran" and "the rule said yes for its own reason".
func TestTheInStockBadgeFollowsTheDefinition(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		manage    bool
		backorder bool
		inventory query.Record
		want      bool
	}{
		{
			name: "an uncounted variant is always sellable",
			// manage_inventory false: the merchant keeps no number for it, so
			// the absent inventory record is not an obstacle.
			manage: false, inventory: nil, want: true,
		},
		{
			name:   "a counted variant with backorder is sellable at zero",
			manage: true, backorder: true,
			inventory: query.Record{"available_quantity": int64(0)},
			want:      true,
		},
		{
			name:   "a counted variant with stock is sellable",
			manage: true,
			inventory: query.Record{
				"id": "invitem_1", "available_quantity": int64(3),
			},
			want: true,
		},
		{
			name:      "a counted variant at zero is not",
			manage:    true,
			inventory: query.Record{"available_quantity": int64(0)},
			want:      false,
		},
		{
			name: "a counted variant with NO inventory link is not",
			// The link is optional and nothing forces it. Answering true here
			// would sell what the shop may not have.
			manage: true, inventory: nil, want: false,
		},
		{
			name:   "a counted variant whose record carries no quantity is not",
			manage: true,
			// The shape a renamed inventory field produces. The answer errs
			// towards hiding stock, never towards selling it.
			inventory: query.Record{"id": "invitem_1", "stocked_quantity": int64(9)},
			want:      false,
		},
		{
			name:      "a quantity that arrived through JSON is still read",
			manage:    true,
			inventory: query.Record{"available_quantity": float64(4)},
			want:      true,
		},
		{
			name:      "a quantity decoded with UseNumber is still read",
			manage:    true,
			inventory: query.Record{"available_quantity": json.Number("4")},
			want:      true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fx := newBadgeFixture(t, variantSpec{
				manage: tc.manage, backorder: tc.backorder, inventory: tc.inventory,
			})

			result, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{})
			require.NoError(t, err)
			require.Len(t, result.Items, 1)
			require.Len(t, result.Items[0].Variants, 1)

			assert.Equal(t, tc.want, result.Items[0].Variants[0].InStock,
				"the variant badge must follow ADR 0040's definition")
			assert.Equal(t, tc.want, result.Items[0].InStock,
				"a one-variant product carries its variant's answer")
		})
	}
}

// TestAProductWithNoSellableVariantIsOutOfStock pins the aggregation, which the
// single-variant table above cannot separate from the variant rule.
func TestAProductWithNoSellableVariantIsOutOfStock(t *testing.T) {
	t.Parallel()

	fx := newBadgeFixture(t,
		variantSpec{manage: true, inventory: query.Record{"available_quantity": int64(0)}},
		variantSpec{manage: true, inventory: nil},
	)

	result, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)

	assert.False(t, result.Items[0].InStock,
		"no variant is sellable, so the product is not in stock")
}

// TestOneSellableVariantMakesTheProductInStock is the other half: the product
// answer is an OR and not an AND.
func TestOneSellableVariantMakesTheProductInStock(t *testing.T) {
	t.Parallel()

	fx := newBadgeFixture(t,
		variantSpec{manage: true, inventory: query.Record{"available_quantity": int64(0)}},
		variantSpec{manage: false},
	)

	result, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)

	assert.True(t, result.Items[0].InStock,
		"a product is offered when SOMETHING under it is offered")
	assert.False(t, result.Items[0].Variants[0].InStock,
		"the variant answers for itself and is not overwritten by the product's")
}

// TestAProductWithNoVariantsIsNotInStock covers the clause with nothing to
// aggregate: there is nothing to sell.
func TestAProductWithNoVariantsIsNotInStock(t *testing.T) {
	t.Parallel()

	graph := &fakeGraph{}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)

	_, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Handle: "poster", Title: "Poster", Status: models.StatusPublished,
		Variants: []service.CreateVariantInput{},
	})
	require.NoError(t, err)

	result, err := svc.ListStoreProducts(context.Background(), service.StoreListOptions{})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Empty(t, result.Items[0].Variants)

	assert.False(t, result.Items[0].InStock, "there is nothing under it to sell")
}

// --- ADR 0040: the filter -----------------------------------------------

// TestTheInStockFilterKeepsWhatCanBeBought covers both directions of the
// parameter in one fixture, which is what makes the pair meaningful: a filter
// tested only in the true direction passes just as well when it keeps
// everything.
func TestTheInStockFilterKeepsWhatCanBeBought(t *testing.T) {
	t.Parallel()

	fx := newScanFixture(t, 4, func(i int) bool { return i%2 == 0 })

	inStock, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		InStock: ptr(true),
	})
	require.NoError(t, err)
	assert.Equal(t, 2, len(inStock.Items), "half of the fixture is sellable")
	for i := range inStock.Items {
		assert.True(t, inStock.Items[i].InStock)
	}

	outOfStock, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		InStock: ptr(false),
	})
	require.NoError(t, err)
	assert.Equal(t, 2, len(outOfStock.Items), "the other half is not")
	for i := range outOfStock.Items {
		assert.False(t, outOfStock.Items[i].InStock)
	}
}

// TestAFilteredPageIsFilledRatherThanCutShort is the property the scan exists
// for.
//
// The naive build applies the filter to the page the database already cut, and
// on this fixture it would hand back one product for a limit of two. That is not
// merely ugly: with a sparse match an EMPTY page appears in the middle of the
// catalog, and a client that stops on an empty page loses everything beyond it.
func TestAFilteredPageIsFilledRatherThanCutShort(t *testing.T) {
	t.Parallel()

	// Only every fourth product is sellable, so a page of two spans eight
	// catalog rows and no single database page could produce it.
	fx := newScanFixture(t, 12, func(i int) bool { return i%4 == 0 })

	page, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		InStock: ptr(true), Limit: 2,
	})
	require.NoError(t, err)

	assert.Len(t, page.Items, 2, "the page must be filled from beyond the first database page")
	assert.NotEmpty(t, page.NextCursor, "a third match is still ahead")
}

// TestPagingAFilteredListingLosesAndRepeatsNothing walks the whole catalog by
// cursor and compares the walk against the answer the same filter gives in one
// call.
//
// It is the assertion that catches a resume point taken from the wrong row:
// resuming after the CHUNK loses the matches the chunk still held, and resuming
// before the last match returns it twice. Both look like a working filter until
// somebody counts.
func TestPagingAFilteredListingLosesAndRepeatsNothing(t *testing.T) {
	t.Parallel()

	fx := newScanFixture(t, 12, func(i int) bool { return i%3 == 0 })

	whole, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		InStock: ptr(true), Limit: 100,
	})
	require.NoError(t, err)
	require.Len(t, whole.Items, 4)

	var (
		walked []string
		cursor = ""
	)
	for range 10 {
		opts := service.StoreListOptions{InStock: ptr(true), Limit: 1}
		if cursor != "" {
			decoded, err := decodeCursor(cursor)
			require.NoError(t, err)
			opts.After = decoded
		}

		page, err := fx.svc.ListStoreProducts(context.Background(), opts)
		require.NoError(t, err)

		for i := range page.Items {
			walked = append(walked, page.Items[i].ID)
		}

		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}

	assert.Equal(t, "", cursor, "the walk must reach the end of the catalog")

	expected := make([]string, 0, len(whole.Items))
	for i := range whole.Items {
		expected = append(expected, whole.Items[i].ID)
	}
	assert.Equal(t, expected, walked,
		"paging one at a time must produce exactly what one call produces")
}

// TestAFilteredListingIsNotCounted pins the consequence the endpoint documents.
//
// Count nil means "not counted", never "zero records"; the number cannot be
// produced without enriching the whole catalog, and returning the count of the
// SQL-filtered set instead would be a number that describes a different set.
func TestAFilteredListingIsNotCounted(t *testing.T) {
	t.Parallel()

	fx := newScanFixture(t, 4, func(i int) bool { return i%2 == 0 })

	page, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		InStock: ptr(true),
	})
	require.NoError(t, err)

	assert.Nil(t, page.Count,
		"the matches of an enriched filter cannot be counted without walking the whole catalog")

	unfiltered, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{})
	require.NoError(t, err)
	assert.Equal(t, 4, requireCount(t, unfiltered),
		"the unfiltered listing still counts, exactly as it did before the filters existed")
}

// TestAnOffsetIsRefusedBesideAnEnrichedFilter keeps the paging honest.
//
// An offset counts rows the DATABASE returns; the set the client sees is chosen
// after that, so honoring both would skip catalog rows rather than matches and
// the second page would begin somewhere the first had already shown.
func TestAnOffsetIsRefusedBesideAnEnrichedFilter(t *testing.T) {
	t.Parallel()

	fx := newScanFixture(t, 4, func(int) bool { return true })

	_, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		InStock: ptr(true), Offset: 2,
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err),
		"the refusal is a validation error, not a server fault")

	_, err = fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{Offset: 2})
	require.NoError(t, err, "an offset without such a filter is untouched")
}

// TestTheScanStopsAtItsBudgetAndHandsBackACursor covers the case the budget
// exists for: nothing matches for a long stretch.
//
// The page comes back SHORT and the cursor is NOT empty, which is what tells a
// client to keep going. An empty cursor here would say "the catalog is
// exhausted" about a catalog the scan merely stopped walking.
func TestTheScanStopsAtItsBudgetAndHandsBackACursor(t *testing.T) {
	t.Parallel()

	// Nothing in the first budget's worth of rows matches.
	fx := newScanFixture(t, 600, func(i int) bool { return i >= 550 })

	page, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		InStock: ptr(true), Limit: 5,
	})
	require.NoError(t, err)

	assert.Empty(t, page.Items, "the budget ran out before a match was found")
	assert.NotEmpty(t, page.NextCursor,
		"a short page with a cursor is 'keep going'; an empty cursor would be a lie about the end")
}

// TestAFullPageCarriesACursorOnlyWhileTheCatalogHasMore covers the ONE line that
// decides whether a filtered listing says "the catalog is exhausted".
//
// The scan has two ways out and only one of them was exercised before this test:
// every fixture in this file terminated through an UNFILLED chunk, so the branch
// that runs when a page fills EXACTLY on the catalog's last row had no test at
// all. Verified by mutation on 2026-09-08 -- replacing the condition with `true`
// (always hand back a cursor) left the whole unit suite and the e2e lane green.
//
// The three cases below are the three answers that line can give, and each one
// fails under a different mutation of it:
//
//  1. the page fills on the last row of the catalog: NO cursor, because an empty
//     next_cursor is the only end-of-catalog signal the ADR, the OpenAPI
//     description and the GraphQL schema all promise. A cursor here would give
//     every client a phantom "more results" and one extra empty request on every
//     full last page;
//  2. the page fills with matches still left in the SAME chunk: a cursor, or
//     those matches are lost;
//  3. the page fills on the last row OF THE CHUNK while the listing has more
//     chunks: a cursor, for the same reason.
func TestAFullPageCarriesACursorOnlyWhileTheCatalogHasMore(t *testing.T) {
	t.Parallel()

	t.Run("a page that fills on the catalog's last row ends the walk", func(t *testing.T) {
		t.Parallel()

		fx := newScanFixture(t, 3, func(int) bool { return true })

		page, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
			InStock: ptr(true), Limit: 3,
		})
		require.NoError(t, err)

		require.Len(t, page.Items, 3, "the page has to be FULL, or it is the other branch")
		assert.Empty(t, page.NextCursor,
			"the last match is the catalog's last row; a cursor here promises a page that "+
				"does not exist")
	})

	t.Run("a page that fills with matches left in the chunk keeps going", func(t *testing.T) {
		t.Parallel()

		fx := newScanFixture(t, 4, func(int) bool { return true })

		page, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
			InStock: ptr(true), Limit: 3,
		})
		require.NoError(t, err)

		require.Len(t, page.Items, 3)
		assert.NotEmpty(t, page.NextCursor,
			"the fourth match sits in the chunk this page stopped in the middle of")
	})

	t.Run("a page that fills on the chunk's last row keeps going", func(t *testing.T) {
		t.Parallel()

		// One product MORE than a chunk, so the page fills on the chunk's final
		// row while the listing still reports a next page. That is the other
		// half of the condition and no fixture above reaches it.
		fx := newScanFixture(t, service.MaxLimit+1, func(int) bool { return true })

		page, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
			InStock: ptr(true), Limit: service.MaxLimit,
		})
		require.NoError(t, err)

		require.Len(t, page.Items, service.MaxLimit)
		require.NotEmpty(t, page.NextCursor,
			"the chunk ended, the catalog did not")

		cursor, err := decodeCursor(page.NextCursor)
		require.NoError(t, err)

		rest, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
			InStock: ptr(true), Limit: service.MaxLimit, After: cursor,
		})
		require.NoError(t, err)

		assert.Len(t, rest.Items, 1, "the cursor has to lead to the product left over")
		assert.Empty(t, rest.NextCursor, "and that one ends the walk")
	})
}

// TestTheScansCursorIsMintedUnderTheOrderItWasAskedFor is the scan's half of the
// trap [service.ProductListingFor] exists for.
//
// The two orders walk the same key in opposite directions, so a cursor minted
// under one is a perfectly valid POSITION in the other's key space and would
// quietly serve the wrong page. The listing name carries the order so that
// corepage.Decode refuses it instead.
//
// The unfiltered path was covered against the database; the SCAN was not.
// Verified by mutation on 2026-09-08 -- replacing ProductListingFor(order) with
// the bare ProductListing inside scanStoreProducts left the unit suite,
// internal/e2e and the product integration lane all green, while an
// in_stock + sort=oldest client got a cursor its own next request would be
// refused for.
//
// What this test pins is the cursor's NAME and that the walk it drives is
// complete, not the DIRECTION of the rows: the fake store orders by id in both
// directions (see memStore.ListProducts), because a cursor's name is a service
// property while the order is the database's. The direction is pinned against a
// real Postgres in TestPagingByCursorCoversTheSetInBothOrders.
func TestTheScansCursorIsMintedUnderTheOrderItWasAskedFor(t *testing.T) {
	t.Parallel()

	fx := newScanFixture(t, 12, func(i int) bool { return i%3 == 0 })

	first, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		InStock: ptr(true), Limit: 1, Order: models.ProductOrderOldest,
	})
	require.NoError(t, err)
	require.Len(t, first.Items, 1)
	require.NotEmpty(t, first.NextCursor)

	_, err = corepage.Decode(service.ProductListingFor(models.ProductOrderNewest), first.NextCursor)
	require.Error(t, err,
		"a cursor the scan minted under sort=oldest must NOT decode as a newest-first one; "+
			"if it does, the scan named it after the wrong listing and the client's own next "+
			"request is the thing that gets refused")

	// The whole walk, to show the cursor is not merely well NAMED but usable:
	// paging one at a time under sort=oldest has to produce exactly what one
	// call under the same order produces.
	var (
		walked []string
		cursor = ""
	)

	for range 10 {
		opts := service.StoreListOptions{
			InStock: ptr(true), Limit: 1, Order: models.ProductOrderOldest,
		}
		if cursor != "" {
			decoded, decodeErr := corepage.Decode(
				service.ProductListingFor(models.ProductOrderOldest), cursor)
			require.NoError(t, decodeErr)

			opts.After = decoded
		}

		page, listErr := fx.svc.ListStoreProducts(context.Background(), opts)
		require.NoError(t, listErr)

		for i := range page.Items {
			walked = append(walked, page.Items[i].ID)
		}

		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}

	whole, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		InStock: ptr(true), Limit: 100, Order: models.ProductOrderOldest,
	})
	require.NoError(t, err)
	require.Len(t, whole.Items, 4)

	expected := make([]string, 0, len(whole.Items))
	for i := range whole.Items {
		expected = append(expected, whole.Items[i].ID)
	}

	assert.Equal(t, expected, walked,
		"paging one at a time under sort=oldest must produce exactly what one call produces")
}

// --- ADR 0039: the option value filter ----------------------------------

// TestTheOptionValueFilterMatchesOnTheFoldedForm is the decision's whole point:
// two spellings of one value meet.
func TestTheOptionValueFilterMatchesOnTheFoldedForm(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), &fakeGraph{})
	seedProductInput(t, svc, service.CreateProductInput{
		Handle: "tee", Title: "Tee", Status: models.StatusPublished,
		// The dotted and dotless spellings of one Turkish word are the case the
		// fold exists for. The value is written with \u escapes so this file
		// carries no Turkish letter of its own (ADR 0012): it is "k", the
		// dotless i (U+0131), "rm", the dotless i again, "z" and once more.
		Options:  []service.CreateOptionInput{{Title: "Renk", Values: []string{turkishRed}}},
		Variants: []service.CreateVariantInput{{Title: "One size", Options: map[string]string{"Renk": turkishRed}}},
	})
	seedProductInput(t, svc, service.CreateProductInput{
		Handle: "cap", Title: "Cap", Status: models.StatusPublished,
		Options:  []service.CreateOptionInput{{Title: "Renk", Values: []string{"Mavi"}}},
		Variants: []service.CreateVariantInput{{Title: "One size", Options: map[string]string{"Renk": "Mavi"}}},
	})

	for _, spelling := range []string{"KIRMIZI", "  kirmizi  ", turkishRed} {
		t.Run(spelling, func(t *testing.T) {
			t.Parallel()

			page, err := svc.ListStoreProducts(context.Background(), service.StoreListOptions{
				OptionValue: ptr(spelling),
			})
			require.NoError(t, err)
			require.Len(t, page.Items, 1, "every spelling of the one value must find the one product")
			assert.Equal(t, "tee", page.Items[0].Handle)
		})
	}
}

// TestTheOptionValueFilterDoesNotMergeTwoValuesItCannotFold is the half that
// decided the fold's shape.
//
// slugify -- the fold this repository applies to handles -- drops every rune it
// cannot transliterate, so two Cyrillic values both fold to the empty string and
// become one value. FoldOptionValue keeps them, so they stay apart: the filter
// misses an unaccented approximation, which is a MISS, rather than reporting one
// value's products under another value's name, which is a wrong answer nobody
// can see.
func TestTheOptionValueFilterDoesNotMergeTwoValuesItCannotFold(t *testing.T) {
	t.Parallel()

	const (
		red  = "\u041a\u0440\u0430\u0441\u043d\u044b\u0439" // Krasnyy, in Cyrillic
		blue = "\u0421\u0438\u043d\u0438\u0439"             // Siniy, in Cyrillic
	)

	svc := newService(t, newMemStore(), newFakeLinker(), &fakeGraph{})
	seedProductInput(t, svc, service.CreateProductInput{
		Handle: "shirt-red", Title: "Red shirt", Status: models.StatusPublished,
		Options:  []service.CreateOptionInput{{Title: "Color", Values: []string{red}}},
		Variants: []service.CreateVariantInput{{Title: "One size", Options: map[string]string{"Color": red}}},
	})
	seedProductInput(t, svc, service.CreateProductInput{
		Handle: "shirt-blue", Title: "Blue shirt", Status: models.StatusPublished,
		Options:  []service.CreateOptionInput{{Title: "Color", Values: []string{blue}}},
		Variants: []service.CreateVariantInput{{Title: "One size", Options: map[string]string{"Color": blue}}},
	})

	page, err := svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		OptionValue: ptr(red),
	})
	require.NoError(t, err)
	require.Len(t, page.Items, 1, "two different values must not fold into one")
	assert.Equal(t, "shirt-red", page.Items[0].Handle)
}

// TestTheOptionValueFilterIsCountedAndPagedByTheDatabase separates it from the
// other two filters, which is the distinction this surface is built around.
//
// It is a predicate over this module's own tables, so it belongs in the WHERE
// clause -- and the observable consequence of being there is that the count
// describes the FILTERED set and the offset counts matches.
func TestTheOptionValueFilterIsCountedAndPagedByTheDatabase(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), &fakeGraph{})
	for i := range 3 {
		seedProductInput(t, svc, service.CreateProductInput{
			Handle: fmt.Sprintf("red-%d", i), Title: "Red", Status: models.StatusPublished,
			Options:  []service.CreateOptionInput{{Title: "Color", Values: []string{"Red"}}},
			Variants: []service.CreateVariantInput{{Title: "One size", Options: map[string]string{"Color": "Red"}}},
		})
	}
	seedProductInput(t, svc, service.CreateProductInput{
		Handle: "blue", Title: "Blue", Status: models.StatusPublished,
		Options:  []service.CreateOptionInput{{Title: "Color", Values: []string{"Blue"}}},
		Variants: []service.CreateVariantInput{{Title: "One size", Options: map[string]string{"Color": "Blue"}}},
	})

	page, err := svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		OptionValue: ptr("red"), Limit: 2,
	})
	require.NoError(t, err)

	assert.Len(t, page.Items, 2)
	assert.Equal(t, 3, requireCount(t, page),
		"the count must describe the filtered set, which is what a WHERE clause buys")

	second, err := svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		OptionValue: ptr("red"), Limit: 2, Offset: 2,
	})
	require.NoError(t, err)
	assert.Len(t, second.Items, 1, "the offset counts MATCHES, not catalog rows")
}

// --- ADR 0041: the price filter -----------------------------------------

// TestThePriceFilterComparesTheBasePriceAtQuantityOne walks the THREE points of
// the decision that a table of prices can rule on: the currency (point 1), the
// base price (point 2) and quantity tier one (point 3).
//
// Each row carries ONE price the catalog holds and asks whether a bracket that
// would contain its amount actually matches. Every "false" row is a price whose
// AMOUNT is inside the bracket and which is refused for one of those three
// reasons, so a build that compared amounts and ignored them would fail every
// one of those rows.
//
// Points 4 and 5 are NOT tested here and cannot be: "no customer-group context"
// is a property of what pricing hands over -- its provider drops every
// conditional price before it answers, so no group price can reach this
// comparison for a row to catch -- and "evaluated at the moment of the query" is
// the absence of a cache. A table of prices cannot falsify either.
func TestThePriceFilterComparesTheBasePriceAtQuantityOne(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		price map[string]any
		want  bool
	}{
		{
			name: "the base price in the request's currency at tier one",
			price: map[string]any{
				"currency_code": "TRY", "amount": int64(15000),
				"min_quantity": int32(1), "price_list_id": (*string)(nil),
			},
			want: true,
		},
		{
			name: "a price belonging to a list is not the base price",
			price: map[string]any{
				"currency_code": "TRY", "amount": int64(15000),
				"min_quantity": int32(1), "price_list_id": ptr("plist_sale"),
			},
			want: false,
		},
		{
			name: "another currency is not compared",
			price: map[string]any{
				"currency_code": "USD", "amount": int64(15000), "min_quantity": int32(1),
			},
			want: false,
		},
		{
			name: "a wholesale tier is invisible to a catalog filter",
			price: map[string]any{
				"currency_code": "TRY", "amount": int64(15000), "min_quantity": int32(50),
			},
			want: false,
		},
		{
			name: "a tier that ends below one unit is invisible too",
			price: map[string]any{
				"currency_code": "TRY", "amount": int64(15000),
				"min_quantity": int32(1), "max_quantity": ptr(int32(0)),
			},
			want: false,
		},
		{
			name: "a bounded tier that covers one unit is compared",
			price: map[string]any{
				"currency_code": "TRY", "amount": int64(15000),
				"min_quantity": int32(1), "max_quantity": ptr(int32(9)),
			},
			want: true,
		},
		{
			name: "a price that arrived through JSON is still compared",
			price: map[string]any{
				"currency_code": "TRY", "amount": float64(15000), "min_quantity": float64(1),
				"price_list_id": nil,
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fx := newPriceFixture(t, tc.price)

			page, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
				Price: &service.PriceBracket{
					CurrencyCode: "TRY", Min: ptr(int64(10000)), Max: ptr(int64(20000)),
				},
			})
			require.NoError(t, err)

			assert.Equal(t, tc.want, len(page.Items) == 1,
				"the bracket contains the amount; only ADR 0041's other points may rule it out")
		})
	}
}

// TestThePriceFilterBoundsAreInclusiveAndOpenEnded covers the interval itself,
// which the point-by-point table above holds constant.
func TestThePriceFilterBoundsAreInclusiveAndOpenEnded(t *testing.T) {
	t.Parallel()

	fx := newPriceFixture(t, map[string]any{
		"currency_code": "TRY", "amount": int64(10000), "min_quantity": int32(1),
	})

	cases := []struct {
		name    string
		bracket service.PriceBracket
		want    bool
	}{
		{"the lower bound is inclusive", service.PriceBracket{CurrencyCode: "TRY", Min: ptr(int64(10000))}, true},
		{"the upper bound is inclusive", service.PriceBracket{CurrencyCode: "TRY", Max: ptr(int64(10000))}, true},
		{"below an open lower bound", service.PriceBracket{CurrencyCode: "TRY", Min: ptr(int64(10001))}, false},
		{"above an open upper bound", service.PriceBracket{CurrencyCode: "TRY", Max: ptr(int64(9999))}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bracket := tc.bracket
			page, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
				Price: &bracket,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.want, len(page.Items) == 1)
		})
	}
}

// TestThePriceFilterFoldsTheCurrencyCase closes an asymmetry the two read
// surfaces would otherwise carry.
//
// REST has to trim the code anyway, to tell "not given" from "given empty", so
// upper-casing it there costs nothing and reads as done. GraphQL takes the
// bracket as an input object and hands it straight on -- so a client sending
// "try" would be compared against pricing's uppercase codes, match nothing, and
// receive an empty catalog with a 200 and no error. That is a filter failing in
// the direction nobody diagnoses, which is why the fold is in the service and
// not at either edge.
func TestThePriceFilterFoldsTheCurrencyCase(t *testing.T) {
	t.Parallel()

	fx := newPriceFixture(t, map[string]any{
		"currency_code": "TRY", "amount": int64(10000), "min_quantity": int32(1),
	})

	for _, written := range []string{"TRY", "try", " Try "} {
		t.Run(written, func(t *testing.T) {
			t.Parallel()

			page, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
				Price: &service.PriceBracket{CurrencyCode: written, Max: ptr(int64(20000))},
			})
			require.NoError(t, err)
			assert.Len(t, page.Items, 1,
				"however the client wrote the code, it names one currency")
		})
	}
}

// TestAVariantPricedOnlyOnAListNeverMatches is a consequence ADR 0041 accepts
// rather than a bug, and it is pinned so that nobody "fixes" it into a fallback.
//
// A fallback to the list price would reintroduce every objection to "the lowest
// price across lists": the product would move in and out of a bracket when a
// sale opens, with nobody having edited anything.
func TestAVariantPricedOnlyOnAListNeverMatches(t *testing.T) {
	t.Parallel()

	fx := newPriceFixture(t, map[string]any{
		"currency_code": "TRY", "amount": int64(9000),
		"min_quantity": int32(1), "price_list_id": ptr("plist_override"),
	})

	page, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		Price: &service.PriceBracket{CurrencyCode: "TRY", Max: ptr(int64(100000))},
	})
	require.NoError(t, err)
	assert.Empty(t, page.Items, "an override-only variant has no base price to compare")
}

// TestThePriceBracketIsValidatedInTheService keeps one definition of what a
// bracket may be.
//
// REST spells it as three flat query parameters and GraphQL as one input
// object; a rule written at either edge would have to be written twice, and the
// two surfaces of one installation would drift into refusing different requests.
func TestThePriceBracketIsValidatedInTheService(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		bracket service.PriceBracket
	}{
		{"a bound without a currency", service.PriceBracket{Min: ptr(int64(1))}},
		{"a currency that is not a code", service.PriceBracket{CurrencyCode: "TURKISH LIRA", Min: ptr(int64(1))}},
		{"a currency without a bound", service.PriceBracket{CurrencyCode: "TRY"}},
		{"a negative lower bound", service.PriceBracket{CurrencyCode: "TRY", Min: ptr(int64(-1))}},
		{"a negative upper bound", service.PriceBracket{CurrencyCode: "TRY", Max: ptr(int64(-1))}},
		{
			"a reversed pair",
			service.PriceBracket{CurrencyCode: "TRY", Min: ptr(int64(500)), Max: ptr(int64(100))},
		},
	}

	svc := newService(t, newMemStore(), newFakeLinker(), &fakeGraph{})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bracket := tc.bracket
			_, err := svc.ListStoreProducts(context.Background(), service.StoreListOptions{Price: &bracket})
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err),
				"an unusable bracket is the client's mistake, not a server fault")
		})
	}
}

// TestTwoEnrichedFiltersNarrowTogether pins that they are ANDed, and with them
// the any-variant rule each of them states on its own.
func TestTwoEnrichedFiltersNarrowTogether(t *testing.T) {
	t.Parallel()

	graph := &fakeGraph{}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)

	cheapAndStocked := seedProduct(t, svc, "cheap-stocked", "Cheap and stocked")
	cheapAndGone := seedProduct(t, svc, "cheap-gone", "Cheap and gone")

	base := map[string]any{"currency_code": "TRY", "amount": int64(5000), "min_quantity": int32(1)}
	graph.records = []query.Record{
		{
			"id":             cheapAndStocked.Variants[0].ID,
			"price_set":      query.Record{"prices": []map[string]any{base}},
			"inventory_item": query.Record{"available_quantity": int64(2)},
		},
		{
			"id":        cheapAndGone.Variants[0].ID,
			"price_set": query.Record{"prices": []map[string]any{base}},
		},
	}

	page, err := svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		InStock: ptr(true),
		Price:   &service.PriceBracket{CurrencyCode: "TRY", Max: ptr(int64(10000))},
	})
	require.NoError(t, err)

	require.Len(t, page.Items, 1, "both criteria have to hold")
	assert.Equal(t, "cheap-stocked", page.Items[0].Handle)
}

// decodeCursor turns a page's NextCursor back into the position the next
// request sends.
//
// It goes through [corepage.Decode] rather than building the struct by hand,
// because the cursor's NAME is half of the contract: a cursor minted under one
// listing order is refused when sent with the other, and a test that skipped the
// decoding would page happily through a key space the cursor does not describe.
func decodeCursor(raw string) (corepage.Cursor, error) {
	return corepage.Decode(service.ProductListingFor(models.ProductOrderNewest), raw)
}

// turkishRed is one Turkish color name written without a single Turkish letter
// in this file (ADR 0012).
//
// Spelled out: "k", the DOTLESS i (U+0131), "rm", the dotless i again, "z", and
// the dotless i a third time. Its other spelling uses the dotted i in all three
// places, and the two meeting is what [models.FoldOptionValue] exists for -- the
// dotless i has no Unicode decomposition, so an NFD-based fold would leave the
// two apart for ever.
const turkishRed = "k\u0131rm\u0131z\u0131"

// --- fixtures -----------------------------------------------------------

// variantSpec describes one variant of a badge fixture.
type variantSpec struct {
	manage    bool
	backorder bool
	inventory query.Record
}

// newBadgeFixture builds one published product whose variants carry the given
// flags, with the inventory records the Query layer will hand back.
func newBadgeFixture(t *testing.T, specs ...variantSpec) storeFixture {
	t.Helper()

	graph := &fakeGraph{}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)

	inputs := make([]service.CreateVariantInput, 0, len(specs))
	for i, spec := range specs {
		inputs = append(inputs, service.CreateVariantInput{
			Title:           fmt.Sprintf("Variant %d", i),
			ManageInventory: ptr(spec.manage),
			AllowBackorder:  ptr(spec.backorder),
		})
	}

	product := seedProductInput(t, svc, service.CreateProductInput{
		Handle: "badge", Title: "Badge", Status: models.StatusPublished, Variants: inputs,
	})
	require.Len(t, product.Variants, len(specs))

	records := make([]query.Record, 0, len(specs))
	for i, spec := range specs {
		record := query.Record{"id": product.Variants[i].ID}
		if spec.inventory != nil {
			record["inventory_item"] = spec.inventory
		}
		records = append(records, record)
	}
	graph.records = records

	return storeFixture{svc: svc, graph: graph, products: []models.Product{product}}
}

// newScanFixture builds n published products, each with one COUNTED variant,
// and gives a positive quantity to the ones inStock reports true for.
//
// The variants are counted on purpose: an uncounted variant is in stock by the
// first clause of ADR 0040 alone, so a fixture built out of those could not tell
// a filter that reads the quantity from one that never looks at it.
func newScanFixture(t *testing.T, n int, inStock func(i int) bool) storeFixture {
	t.Helper()

	graph := &fakeGraph{}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)

	products := make([]models.Product, 0, n)
	records := make([]query.Record, 0, n)

	for i := range n {
		product := seedProductInput(t, svc, service.CreateProductInput{
			Handle: fmt.Sprintf("item-%03d", i),
			Title:  fmt.Sprintf("Item %d", i),
			Status: models.StatusPublished,
			Variants: []service.CreateVariantInput{
				{Title: "One size", ManageInventory: ptr(true)},
			},
		})
		products = append(products, product)

		quantity := int64(0)
		if inStock(i) {
			quantity = 5
		}
		records = append(records, query.Record{
			"id":             product.Variants[0].ID,
			"inventory_item": query.Record{"available_quantity": quantity},
		})
	}

	// The fake answers every Graph call with the same slice, which is what the
	// scan needs: it asks for one chunk of variant ids at a time and reads the
	// records back by id, so a superset is exactly what a real provider hands
	// over for the ids it was given.
	graph.records = records

	return storeFixture{svc: svc, graph: graph, products: products}
}

// newPriceFixture builds one published product whose single variant carries one
// price, given as pricing's own sub-record shape.
func newPriceFixture(t *testing.T, price map[string]any) storeFixture {
	t.Helper()

	graph := &fakeGraph{}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)

	product := seedProduct(t, svc, "priced", "Priced")
	graph.records = []query.Record{{
		"id":        product.Variants[0].ID,
		"price_set": query.Record{"id": "pset_1", "prices": []map[string]any{price}},
	}}

	return storeFixture{svc: svc, graph: graph, products: []models.Product{product}}
}
