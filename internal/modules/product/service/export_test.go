package service_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// exportGraph answers the export's two reads: the regions, and the price sets
// of the variants asked for, each shaped as its module writes it.
type exportGraph struct {
	mu         sync.Mutex
	currencies []string
	// prices are each variant's price sub-records.
	prices    map[string][]map[string]any
	regionErr error
	priceErr  error
	reads     int
}

func (g *exportGraph) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reads++

	switch spec.Entity {
	case "region":
		if g.regionErr != nil {
			return nil, g.regionErr
		}
		if spec.Offset > 0 {
			return nil, nil
		}
		out := make([]query.Record, 0, len(g.currencies))
		for _, code := range g.currencies {
			out = append(out, query.Record{"currency_code": code})
		}
		return out, nil
	case service.EntityVariant:
		if g.priceErr != nil {
			return nil, g.priceErr
		}
		ids, _ := spec.Filters["ids"].([]string)
		out := make([]query.Record, 0, len(ids))
		for _, id := range ids {
			record := query.Record{query.IDField: id}
			if prices, ok := g.prices[id]; ok {
				record["price_set"] = query.Record{"id": "pset_" + id, "prices": prices}
			}
			out = append(out, record)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unexpected entity %q", spec.Entity)
	}
}

// price is one price sub-record as pricing writes it.
func price(currency string, amount int64, list *string, minQuantity int32, maxQuantity *int32) map[string]any {
	return map[string]any{
		"currency_code": currency, "amount": amount, "price_list_id": list,
		"min_quantity": minQuantity, "max_quantity": maxQuantity,
	}
}

// exported reads the export back as rows keyed by column name.
func exported(t *testing.T, svc *service.Service, opts service.ExportOptions) (header []string, rows []map[string]string, pages int) {
	t.Helper()

	var out bytes.Buffer
	require.NoError(t, svc.ExportProducts(context.Background(), &out, opts, func() error {
		pages++
		return nil
	}))
	records, err := csv.NewReader(&out).ReadAll()
	require.NoError(t, err, "the export is not CSV the standard reader can read")
	require.NotEmpty(t, records)
	header = records[0]
	for _, record := range records[1:] {
		require.Len(t, record, len(header))
		row := map[string]string{}
		for i, column := range header {
			row[column] = record[i]
		}
		rows = append(rows, row)
	}

	return header, rows, pages
}

// TestTheCatalogLeavesAsCSV is ADR 0204's file: a row per variant, a row for a
// product with none, the options and ids in their cells, and each variant's
// base price at one unit in a column per currency a region sells in.
func TestTheCatalogLeavesAsCSV(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	graph := &exportGraph{currencies: []string{"TRY", "eur", "TRY"}, prices: map[string][]map[string]any{}}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)

	shirt, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title: "Shirt", Status: models.StatusPublished,
		Options: []service.CreateOptionInput{{Title: "Size", Values: []string{"S", "M"}}},
		Variants: []service.CreateVariantInput{
			{Title: "Small", SKU: ptr("SHIRT-S"), Options: map[string]string{"Size": "S"}},
			{Title: "Medium", SKU: ptr("SHIRT-M"), Options: map[string]string{"Size": "M"}},
		},
	})
	require.NoError(t, err)
	gift, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title: "=HYPERLINK(\"http://x\")", Status: models.StatusDraft,
	})
	require.NoError(t, err)

	small, medium := shirt.Variants[0], shirt.Variants[1]
	if small.Title != "Small" {
		small, medium = medium, small
	}
	sale := "plist_sale"
	ten := int32(10)
	// The base price comes LAST, so reading the first price in a currency
	// cannot pass for choosing the base one.
	graph.prices[small.ID] = []map[string]any{
		price("TRY", 9900, &sale, 1, nil),
		price("TRY", 11000, nil, 10, nil),
		price("EUR", 1500, nil, 1, &ten),
		price("TRY", 12900, nil, 1, nil),
	}

	header, rows, pages := exported(t, svc, service.ExportOptions{})

	assert.Equal(t, []string{"variant_price_eur", "variant_price_try", "variant_cost_eur", "variant_cost_try"},
		header[len(header)-4:],
		"one price column per currency a region sells in, sorted, once, then a cost column for each (ADR 0424)")
	require.Len(t, rows, 3, "two variants and a product with none")
	assert.Equal(t, 1, pages)

	byVariant := map[string]map[string]string{}
	for _, row := range rows {
		byVariant[row["variant_id"]] = row
	}
	row := byVariant[small.ID]
	require.NotNil(t, row)
	assert.Equal(t, shirt.ID, row["product_id"])
	assert.Equal(t, "SHIRT-S", row["variant_sku"])
	assert.JSONEq(t, `{"Size":"S"}`, row["variant_options"])
	assert.Equal(t, "12900", row["variant_price_try"], "the base price, not the sale or the tier of ten")
	assert.Equal(t, "1500", row["variant_price_eur"], "a tier that covers one unit is the price at one unit")
	assert.Empty(t, byVariant[medium.ID]["variant_price_try"], "no price is an empty cell")

	bare := byVariant[""]
	require.NotNil(t, bare)
	assert.Equal(t, gift.ID, bare["product_id"])
	assert.Equal(t, `'=HYPERLINK("http://x")`, bare["product_title"], "a formula is written as text")
	assert.Equal(t, "draft", bare["product_status"])
}

// TestAnExportReadsEveryPageOnce holds the cursor: a catalog of more than one
// page is read page by page, every product once.
func TestAnExportReadsEveryPageOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(t, newMemStore(), newFakeLinker(), &exportGraph{})
	for i := range 101 {
		_, err := svc.CreateProduct(ctx, service.CreateProductInput{Title: fmt.Sprintf("Product %03d", i)})
		require.NoError(t, err)
	}

	_, rows, pages := exported(t, svc, service.ExportOptions{})

	assert.Equal(t, 2, pages)
	require.Len(t, rows, 101)
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row["product_id"])
	}
	slices.Sort(ids)
	assert.Len(t, slices.Compact(ids), 101, "no product is written twice")
}

// TestAnExportCanBeNarrowedToAStatus exports only the products in it.
func TestAnExportCanBeNarrowedToAStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(t, newMemStore(), newFakeLinker(), &exportGraph{})
	_, err := svc.CreateProduct(ctx, service.CreateProductInput{Title: "Live", Status: models.StatusPublished})
	require.NoError(t, err)
	_, err = svc.CreateProduct(ctx, service.CreateProductInput{Title: "Draft", Status: models.StatusDraft})
	require.NoError(t, err)

	draft := models.StatusDraft
	_, rows, _ := exported(t, svc, service.ExportOptions{Status: &draft})

	require.Len(t, rows, 1)
	assert.Equal(t, "Draft", rows[0]["product_title"])
}

// TestAnExportThatCannotStartWritesNothing keeps a failure before the first
// row an error the caller can still answer with a status.
func TestAnExportThatCannotStartWritesNothing(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(),
		&exportGraph{regionErr: errors.Unavailable("region_down", "the regions are unreachable")})

	var out bytes.Buffer
	err := svc.ExportProducts(context.Background(), &out, service.ExportOptions{}, nil)

	require.Error(t, err)
	assert.Zero(t, out.Len(), "nothing may be written before the export can start")
}

// TestAnExportWithoutPricingHasNoPrices: an installation without the region
// or the pricing module still exports its catalog.
func TestAnExportWithoutPricingHasNoPrices(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	missing := errors.NotFound("query_provider_not_found", "no provider for region")
	svc := newService(t, newMemStore(), newFakeLinker(), &exportGraph{regionErr: missing})
	_, err := svc.CreateProduct(ctx, service.CreateProductInput{Title: "Alone"})
	require.NoError(t, err)

	header, rows, _ := exported(t, svc, service.ExportOptions{})

	require.Len(t, rows, 1)
	for _, column := range header {
		assert.False(t, strings.HasPrefix(column, "variant_price_"), "no price column without regions")
	}
}
