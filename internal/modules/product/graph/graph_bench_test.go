package graph_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// benchPageSize is the number of products on the benchmarked page.
//
// Twenty-four is an ordinary storefront grid: four rows of six.
const benchPageSize = 24

// benchVariantsPerProduct is how many sellable units each product carries.
const benchVariantsPerProduct = 3

// benchDocument is the query a storefront listing page actually sends.
//
// It selects down to the variant and its price set, because that nesting is
// where the response encoding cost lives; a query asking only for ids would
// measure the transport and nothing else.
const benchDocument = `{
  products(limit: 24) {
    count
    offset
    items {
      id handle title subtitle thumbnail createdAt
      variants { id title sku rank priceSet }
    }
  }
}`

// benchStorefront answers from a page built once.
//
// The fake the tests use records the options of every call into three slices,
// which in a benchmark would be measuring the fake.
type benchStorefront struct {
	list service.ListResult[service.StoreProduct]
}

// ListStoreProducts returns the prepared page.
func (s *benchStorefront) ListStoreProducts(
	_ context.Context, _ service.StoreListOptions,
) (service.ListResult[service.StoreProduct], error) {
	return s.list, nil
}

// GetStoreProduct returns the first product of the prepared page.
func (s *benchStorefront) GetStoreProduct(
	_ context.Context, _ string, _ []string,
) (service.StoreProduct, error) {
	return s.list.Items[0], nil
}

// benchCatalogue builds the page the benchmark serves.
func benchCatalogue() *benchStorefront {
	at := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	subtitle := "a subtitle of the length a real one has"

	items := make([]service.StoreProduct, 0, benchPageSize)
	for i := range benchPageSize {
		suffix := strconv.Itoa(i)

		variants := make([]service.StoreVariant, 0, benchVariantsPerProduct)
		for v := range benchVariantsPerProduct {
			variants = append(variants, service.StoreVariant{
				Variant: models.Variant{
					ID:        "var_" + suffix + "_" + strconv.Itoa(v),
					ProductID: "prod_" + suffix,
					Title:     "Size " + strconv.Itoa(v),
					SKU:       ptrOf("SKU-" + suffix + "-" + strconv.Itoa(v)),
					Rank:      int32(v),
					CreatedAt: at,
					UpdatedAt: at,
				},
				PriceSet: query.Record{
					"id":       "pset_" + suffix + "_" + strconv.Itoa(v),
					"amount":   int64(19_900 + i*100),
					"currency": "TRY",
				},
			})
		}

		items = append(items, service.StoreProduct{
			Product: models.Product{
				ID:        "prod_" + suffix,
				Handle:    "product-" + suffix,
				Title:     "Product " + suffix,
				Subtitle:  &subtitle,
				Thumbnail: ptrOf("https://cdn.example.test/" + suffix + ".jpg"),
				Status:    models.StatusPublished,
				CreatedAt: at,
				UpdatedAt: at,
			},
			Variants: variants,
		})
	}

	count := len(items)

	return &benchStorefront{list: service.ListResult[service.StoreProduct]{Items: items, Count: &count}}
}

// ptrOf returns a pointer to the value.
func ptrOf[T any](v T) *T { return &v }

// BenchmarkStorefrontQuery measures the whole per-request Go cost of the read
// surface: transport, parsing, validation, the limit checks, resolution and the
// response encoding.
//
// This is the one path in the repository where the cost of a request is decided
// by WHOEVER WROTE THE QUERY, which is why it carries seven configurable limits
// — and until now nothing measured what a request costs once those limits have
// let it through. The service is a fake on purpose: this is the Go-side figure,
// and the database side is already measured elsewhere.
func BenchmarkStorefrontQuery(b *testing.B) {
	handler := graph.NewHandler(benchCatalogue(), graph.Options{})
	body := `{"query":` + strconv.Quote(benchDocument) + `}`

	// A benchmark measuring an error response would report a fine number and
	// mean nothing.
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, benchRequest(body))
	if first.Code != http.StatusOK || strings.Contains(first.Body.String(), `"errors"`) {
		b.Fatalf("the fixture does not resolve, so the benchmark would measure the error path: %d %s",
			first.Code, first.Body.String())
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, benchRequest(body))
	}
}

// benchRequest builds the POST the benchmark serves.
func benchRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, graph.Path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	return req
}
