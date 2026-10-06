package service_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// costShirt is a product with two variants, Small and Medium, found by SKU.
func costShirt(t *testing.T, svc *service.Service) (small, medium models.Variant) {
	t.Helper()

	shirt, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Handle: "shirt", Title: "Shirt",
		Options: []service.CreateOptionInput{{Title: "Size", Values: []string{"S", "M"}}},
		Variants: []service.CreateVariantInput{
			{Title: "Small", SKU: ptr("SHIRT-S"), Options: map[string]string{"Size": "S"}},
			{Title: "Medium", SKU: ptr("SHIRT-M"), Options: map[string]string{"Size": "M"}},
		},
	})
	require.NoError(t, err)
	small, medium = shirt.Variants[0], shirt.Variants[1]
	if *small.SKU != "SHIRT-S" {
		small, medium = medium, small
	}

	return small, medium
}

// unitCostsOf is a variant's costs by currency.
func unitCostsOf(t *testing.T, svc *service.Service, variantID string) map[string]int64 {
	t.Helper()

	costs, err := svc.VariantCosts(context.Background(), variantID)
	require.NoError(t, err)
	out := map[string]int64{}
	for _, c := range costs {
		out[c.CurrencyCode] = c.Amount
	}

	return out
}

// TestTheExportCarriesEachVariantsUnitCost is ADR 0424: after the price
// columns comes one variant_cost_<currency> column per currency a region sells
// in, holding the variant's unit cost there and empty when it has none; a cost
// in a currency no region sells in has no column.
func TestTheExportCarriesEachVariantsUnitCost(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(t, newMemStore(), newFakeLinker(), &exportGraph{currencies: []string{"TRY", "USD"}})
	small, medium := costShirt(t, svc)
	_, err := svc.SetVariantCosts(ctx, small.ID, []models.VariantCost{
		{CurrencyCode: "TRY", Amount: 400}, {CurrencyCode: "EUR", Amount: 9},
	})
	require.NoError(t, err)

	var out bytes.Buffer
	require.NoError(t, svc.ExportProducts(ctx, &out, service.ExportOptions{}, nil))
	records, err := csv.NewReader(&out).ReadAll()
	require.NoError(t, err)
	header := records[0]
	require.Equal(t, []string{"variant_price_try", "variant_price_usd", "variant_cost_try", "variant_cost_usd"},
		header[len(header)-4:], "the cost columns follow the price columns, in the same currencies")
	assert.NotContains(t, header, "variant_cost_eur", "no region sells in EUR")

	byVariant := map[string][]string{}
	for _, record := range records[1:] {
		require.Len(t, record, len(header))
		byVariant[record[slicesIndex(header, "variant_id")]] = record
	}
	assert.Equal(t, []string{"400", ""}, byVariant[small.ID][len(header)-2:])
	assert.Equal(t, []string{"", ""}, byVariant[medium.ID][len(header)-2:], "a variant with no cost has empty cells")
}

// TestAnImportWritesTheCostsItsCellsName is ADR 0424: a cost cell writes that
// currency's unit cost, an empty cell leaves it, and the variant's other
// currencies stay; a cost that stands is no change and writes nothing.
func TestAnImportWritesTheCostsItsCellsName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), &exportGraph{})
	small, medium := costShirt(t, svc)
	_, err := svc.SetVariantCosts(ctx, small.ID, []models.VariantCost{
		{CurrencyCode: "TRY", Amount: 400}, {CurrencyCode: "EUR", Amount: 9},
	})
	require.NoError(t, err)
	_, err = svc.SetVariantCosts(ctx, medium.ID, []models.VariantCost{{CurrencyCode: "TRY", Amount: 300}})
	require.NoError(t, err)

	done := importAll(t, svc, csvOf(t,
		[]string{"product_handle", "variant_sku", "variant_cost_try", "variant_cost_usd"},
		[]string{"shirt", "SHIRT-S", "450", "7"},
		[]string{"shirt", "SHIRT-M", "", ""},
	))

	assert.Zero(t, done.RowsFailed, "%v", done.Errors)
	assert.Equal(t, 1, done.RowsUpdated, "a cost is a change of the row's variant; empty cells are none")
	assert.Equal(t, map[string]int64{"TRY": 450, "USD": 7, "EUR": 9}, unitCostsOf(t, svc, small.ID),
		"the cells name TRY and USD, and EUR stays")
	assert.Equal(t, map[string]int64{"TRY": 300}, unitCostsOf(t, svc, medium.ID), "an empty cell writes nothing")

	writes := store.callCount("ReplaceVariantCosts")
	again := importAll(t, svc, csvOf(t,
		[]string{"product_handle", "variant_sku", "variant_cost_try"},
		[]string{"shirt", "SHIRT-S", "450"},
	))
	assert.Zero(t, again.RowsFailed, "%v", again.Errors)
	assert.Zero(t, again.RowsUpdated, "a cost that stands is no change")
	assert.Equal(t, writes, store.callCount("ReplaceVariantCosts"), "and nothing is written")
}

// TestACostTheImportCannotWriteChangesNothing: the row's cost cells are read
// and held to the cost list's rules before anything of the row is written, so
// a row whose cost is refused leaves its product as it was.
func TestACostTheImportCannotWriteChangesNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(t, newMemStore(), newFakeLinker(), &exportGraph{})
	small, _ := costShirt(t, svc)
	full := make([]models.VariantCost, 0, service.MaxVariantCosts)
	for i := range service.MaxVariantCosts {
		full = append(full, models.VariantCost{
			CurrencyCode: string([]byte{'A' + byte(i/26), 'A' + byte(i%26), 'Z'}), Amount: 1,
		})
	}
	_, err := svc.SetVariantCosts(ctx, small.ID, full)
	require.NoError(t, err)

	for name, cell := range map[string]string{
		"not a whole number": "4,5",
		"negative":           "-1",
		"above the bound":    strconv.FormatInt(service.MaxCostAmount+1, 10),
	} {
		t.Run(name, func(t *testing.T) {
			done := importAll(t, svc, csvOf(t,
				[]string{"product_handle", "product_title", "variant_sku", "variant_cost_try"},
				[]string{"shirt", "Shirt, renamed", "SHIRT-S", cell},
			))
			assert.Equal(t, 1, done.RowsFailed, "%v", done.Errors)
		})
	}

	// The bound on currencies is the stored list's, so it is held where the
	// list is written, under the variant's lock: the row is refused and the
	// list is as it was.
	done := importAll(t, svc, csvOf(t,
		[]string{"product_handle", "variant_sku", "variant_cost_try"},
		[]string{"shirt", "SHIRT-S", "500"},
	))
	assert.Equal(t, 1, done.RowsFailed, "a 51st currency is refused, not cut: %v", done.Errors)
	assert.Len(t, unitCostsOf(t, svc, small.ID), service.MaxVariantCosts)
	assert.NotContains(t, unitCostsOf(t, svc, small.ID), "TRY")

	done = importAll(t, svc, csvOf(t,
		[]string{"product_handle", "product_title", "variant_cost_try"},
		[]string{"shirt", "Shirt, renamed", "500"},
	))
	assert.Equal(t, 1, done.RowsFailed, "a cost belongs to a variant: %v", done.Errors)

	product, err := svc.GetProductByHandle(ctx, "shirt")
	require.NoError(t, err)
	assert.Equal(t, "Shirt", product.Title, "no refused row renamed the product")
}

// TestANewProductIsCostedToo: a row creating its product writes its variant's
// costs with it.
func TestANewProductIsCostedToo(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(t, newMemStore(), newFakeLinker(), &exportGraph{})

	done := importAll(t, svc, csvOf(t,
		[]string{"product_handle", "product_title", "variant_title", "variant_sku", "variant_cost_try"},
		[]string{"mug", "Mug", "One size", "MUG-1", "120"},
	))

	assert.Zero(t, done.RowsFailed, "%v", done.Errors)
	assert.Equal(t, 1, done.RowsCreated)
	mug, err := svc.GetProductByHandle(ctx, "mug")
	require.NoError(t, err)
	require.Len(t, mug.Variants, 1)
	assert.Equal(t, map[string]int64{"TRY": 120}, unitCostsOf(t, svc, mug.Variants[0].ID))
}

// TestAnUnchangedExportWithCostsWritesNothing: the export's own file, sent
// back, finds every cost standing.
func TestAnUnchangedExportWithCostsWritesNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMemStore()
	svc := newImportService(t, store, newFakeLinker(), &exportGraph{currencies: []string{"TRY"}}, newFakePrices())
	small, _ := costShirt(t, svc)
	_, err := svc.SetVariantCosts(ctx, small.ID, []models.VariantCost{{CurrencyCode: "TRY", Amount: 400}})
	require.NoError(t, err)
	writes := store.callCount("ReplaceVariantCosts")

	var exported bytes.Buffer
	require.NoError(t, svc.ExportProducts(ctx, &exported, service.ExportOptions{}, nil))
	require.Contains(t, exported.String(), "variant_cost_try", "the export carries the cost column")
	done := importAll(t, svc, exported.Bytes())

	assert.Zero(t, done.RowsFailed, "%v", done.Errors)
	assert.Zero(t, done.RowsUpdated)
	assert.Equal(t, writes, store.callCount("ReplaceVariantCosts"), "nothing was written")
}

// slicesIndex is the position of a column in a header, failing when absent.
func slicesIndex(header []string, column string) int {
	for i, name := range header {
		if name == column {
			return i
		}
	}

	return -1
}
