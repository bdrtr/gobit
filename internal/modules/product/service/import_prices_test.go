package service_test

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// fakePrices is pricing's surface in memory: each price set holds its base
// price at one unit by currency, and a write reports whether it changed one,
// as pricing's does (ADR 0206).
type fakePrices struct {
	mu     sync.Mutex
	sets   map[string]map[string]int64
	next   int
	writes int
	// absent, when set, is what Installed answers.
	absent error
}

func newFakePrices() *fakePrices { return &fakePrices{sets: map[string]map[string]int64{}} }

func (f *fakePrices) Installed(context.Context) error { return f.absent }

func (f *fakePrices) CreateEmptyPriceSet(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	id := fmt.Sprintf("pset_%d", f.next)
	f.sets[id] = map[string]int64{}

	return id, nil
}

func (f *fakePrices) SetUnitBasePrices(_ context.Context, id string, amounts map[string]int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	set, ok := f.sets[id]
	if !ok {
		return false, errors.NotFound("price_set_not_found", "no price set %s", id)
	}
	changed := false
	for currency, amount := range amounts {
		if current, has := set[currency]; !has || current != amount {
			set[currency], changed = amount, true
		}
	}
	if changed {
		f.writes++
	}

	return changed, nil
}

// pricesOf returns the amounts of the price set a variant is linked to.
func pricesOf(t *testing.T, links *fakeLinker, prices *fakePrices, variantID string) map[string]int64 {
	t.Helper()
	ids, err := links.List(context.Background(), service.LinkVariantPriceSet, variantID)
	require.NoError(t, err)
	require.Len(t, ids, 1, "the variant is linked to one price set")
	prices.mu.Lock()
	defer prices.mu.Unlock()

	return prices.sets[ids[0]]
}

// TestAnImportPricesItsVariants is ADR 0207: a variant with a price set has its
// prices written there, and a variant without one is given one.
func TestAnImportPricesItsVariants(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	links, prices := newFakeLinker(), newFakePrices()
	svc := newImportService(t, newMemStore(), links, &exportGraph{}, prices)
	shirt, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "shirt", Title: "Shirt",
		Options: []service.CreateOptionInput{{Title: "Size", Values: []string{"S", "M"}}},
		Variants: []service.CreateVariantInput{
			{Title: "Small", SKU: ptr("SHIRT-S"), Options: map[string]string{"Size": "S"}},
			{Title: "Medium", SKU: ptr("SHIRT-M"), Options: map[string]string{"Size": "M"}},
		},
	})
	require.NoError(t, err)
	small, medium := shirt.Variants[0], shirt.Variants[1]
	if *small.SKU != "SHIRT-S" {
		small, medium = medium, small
	}
	existing, err := prices.CreateEmptyPriceSet(ctx)
	require.NoError(t, err)
	_, err = prices.SetUnitBasePrices(ctx, existing, map[string]int64{"TRY": 10000})
	require.NoError(t, err)
	require.NoError(t, svc.SetVariantPriceSet(ctx, small.ID, existing))

	done := importAll(t, svc, csvOf(t,
		[]string{"product_handle", "variant_sku", "variant_price_try", "variant_price_usd"},
		[]string{"shirt", "SHIRT-S", "12000", "999"},
		[]string{"shirt", "SHIRT-M", "5000", ""},
	))

	assert.Zero(t, done.RowsFailed, "%v", done.Errors)
	assert.Equal(t, 2, done.RowsUpdated, "a price is a change of the row's variant")
	assert.Equal(t, map[string]int64{"TRY": 12000, "USD": 999}, pricesOf(t, links, prices, small.ID))
	assert.Equal(t, map[string]int64{"TRY": 5000}, pricesOf(t, links, prices, medium.ID),
		"an empty cell writes nothing, and a variant without a set is given one")

	again := importAll(t, svc, csvOf(t,
		[]string{"product_handle", "variant_sku", "variant_price_try"},
		[]string{"shirt", "SHIRT-S", "12000"},
	))
	assert.Zero(t, again.RowsUpdated, "a price that stands is no change")
}

// TestANewProductIsPricedToo: a row creating its product creates its variant's
// price set with it.
func TestANewProductIsPricedToo(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	links, prices := newFakeLinker(), newFakePrices()
	svc := newImportService(t, newMemStore(), links, &exportGraph{}, prices)

	done := importAll(t, svc, csvOf(t,
		[]string{"product_handle", "product_title", "variant_title", "variant_sku", "variant_price_try"},
		[]string{"scarf", "Scarf", "One size", "SCARF-1", "4990"},
	))

	assert.Zero(t, done.RowsFailed, "%v", done.Errors)
	assert.Equal(t, 1, done.RowsCreated)
	scarf, err := svc.GetProductByHandle(ctx, "scarf")
	require.NoError(t, err)
	require.Len(t, scarf.Variants, 1)
	assert.Equal(t, map[string]int64{"TRY": 4990}, pricesOf(t, links, prices, scarf.Variants[0].ID))
}

// TestAPriceTheImportCannotReadChangesNothing: the price cells are read before
// the row writes anything, so a refused price leaves its product as it was.
func TestAPriceTheImportCannotReadChangesNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	prices := newFakePrices()
	svc := newImportService(t, newMemStore(), newFakeLinker(), &exportGraph{}, prices)
	_, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "hat", Title: "Hat",
		Variants: []service.CreateVariantInput{{Title: "One size", SKU: ptr("HAT-1")}},
	})
	require.NoError(t, err)

	done := importAll(t, svc, csvOf(t,
		[]string{"product_handle", "product_title", "variant_sku", "variant_price_try"},
		[]string{"hat", "Hat, renamed", "HAT-1", "49.90"},
		[]string{"cap", "Cap", "", "2990"},
	))

	assert.Equal(t, 2, done.RowsFailed)
	require.Len(t, done.Errors, 2)
	assert.Contains(t, done.Errors[0].Message, "variant_price_try is not a whole number of minor units")
	assert.Contains(t, done.Errors[1].Message, "a price belongs to a variant")
	hat, err := svc.GetProductByHandle(ctx, "hat")
	require.NoError(t, err)
	assert.Equal(t, "Hat", hat.Title, "the refused row wrote nothing")
	_, err = svc.GetProductByHandle(ctx, "cap")
	assert.True(t, errors.IsNotFound(err), "a priced row with no variant creates no product")
	assert.Zero(t, prices.writes)
}

// TestAFileWithPricesNeedsPricing: an installation that cannot write prices
// refuses a file naming them when it is sent, not row by row.
func TestAFileWithPricesNeedsPricing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	priced := csvOf(t, []string{"product_handle", "variant_price_try"}, []string{"hat", "100"})
	plain := csvOf(t, []string{"product_handle", "product_title"}, []string{"hat", "Hat"})

	without := newService(t, newMemStore(), newFakeLinker(), &exportGraph{})
	_, err := without.CreateImport(ctx, priced)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, "product_import_prices_unavailable", errors.CodeOf(err))
	_, err = without.CreateImport(ctx, plain)
	require.NoError(t, err, "a file without prices needs no pricing")

	absent := newFakePrices()
	absent.absent = errors.Unavailable("product_prices_unavailable", "not installed")
	unresolved := newImportService(t, newMemStore(), newFakeLinker(), &exportGraph{}, absent)
	_, err = unresolved.CreateImport(ctx, priced)
	require.Error(t, err)
	assert.Equal(t, "product_import_prices_unavailable", errors.CodeOf(err))

	miswired := newFakePrices()
	miswired.absent = errors.Internal("product_module_setup_failed", "wrong surface")
	broken := newImportService(t, newMemStore(), newFakeLinker(), &exportGraph{}, miswired)
	_, err = broken.CreateImport(ctx, priced)
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal), "a wiring fault is not the caller's to fix")

	assert.True(t, service.ImportNamesPrices(priced))
	assert.False(t, service.ImportNamesPrices(plain))
}

// TestAnUnchangedExportWithPricesWritesNothing: the export's prices, sent back
// as they came, find the amounts already standing.
func TestAnUnchangedExportWithPricesWritesNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	links, prices := newFakeLinker(), newFakePrices()
	graph := &exportGraph{currencies: []string{"TRY"}}
	svc := newImportService(t, newMemStore(), links, graph, prices)
	hat, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "hat", Title: "Hat",
		Variants: []service.CreateVariantInput{{Title: "One size", SKU: ptr("HAT-1")}},
	})
	require.NoError(t, err)
	set, err := prices.CreateEmptyPriceSet(ctx)
	require.NoError(t, err)
	_, err = prices.SetUnitBasePrices(ctx, set, map[string]int64{"TRY": 4990})
	require.NoError(t, err)
	require.NoError(t, svc.SetVariantPriceSet(ctx, hat.Variants[0].ID, set))
	graph.prices = map[string][]map[string]any{hat.Variants[0].ID: {
		{"currency_code": "TRY", "amount": int64(4990), "min_quantity": int32(1)},
	}}
	writes := prices.writes

	var exported bytes.Buffer
	require.NoError(t, svc.ExportProducts(ctx, &exported, service.ExportOptions{}, nil))
	require.Contains(t, exported.String(), "4990", "the export carries the price")
	done := importAll(t, svc, exported.Bytes())

	assert.Zero(t, done.RowsFailed, "%v", done.Errors)
	assert.Zero(t, done.RowsUpdated)
	assert.Equal(t, writes, prices.writes, "nothing was written")
}
