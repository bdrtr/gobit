//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// revalidate reads a storefront address with the harness key and, when tag is
// not empty, an If-None-Match naming it.
func revalidate(t *testing.T, path, tag string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	req.Header.Set(corehttp.PublishableKeyHeader, publishableKey)
	if tag != "" {
		req.Header.Set("If-None-Match", tag)
	}
	rec := httptest.NewRecorder()
	testRouter.ServeHTTP(rec, req)

	return rec
}

// currentTag reads the address and returns the tag its 200 carries.
func currentTag(t *testing.T, path string) string {
	t.Helper()

	rec := revalidate(t, path, "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	tag := rec.Header().Get("ETag")
	require.NotEmpty(t, tag, "%s must answer with a tag", path)

	return tag
}

// TestAProductPageRevalidatesAcrossModules is ADR 0391 on the production
// wiring: a product page built from four modules' records answers the tag it
// gave with 304, and a write in any of them — one that bumps no product version
// among them — moves the tag.
//
// The harness sets no TTL, so this is also the proof that the tag is written at
// the default.
func TestAProductPageRevalidatesAcrossModules(t *testing.T) {
	ctx := t.Context()
	seq := fixtureCounter.Add(1)

	collection, err := productSvc.CreateCollection(ctx, productsvc.CreateCollectionInput{
		Title: "Revalidation", Handle: fmt.Sprintf("e2e-revalidation-%d", seq),
	})
	require.NoError(t, err)
	active := true
	category, err := productSvc.CreateCategory(ctx, productsvc.CreateCategoryInput{
		Name: "Revalidation", Handle: fmt.Sprintf("e2e-revalidation-%d", seq), IsActive: &active,
	})
	require.NoError(t, err)
	kept, err := productSvc.CreateTag(ctx, fmt.Sprintf("e2e-kept-%d", seq))
	require.NoError(t, err)
	removed, err := productSvc.CreateTag(ctx, fmt.Sprintf("e2e-removed-%d", seq))
	require.NoError(t, err)

	product, err := productSvc.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle:       fmt.Sprintf("e2e-revalidation-%d", seq),
		Title:        "Revalidation",
		Status:       productmodels.StatusPublished,
		CollectionID: &collection.ID,
		TagIDs:       []string{kept.ID, removed.ID},
		CategoryIDs:  []string{category.ID},
		Images: []productsvc.CreateImageInput{
			{URL: "https://example.test/revalidation-front.jpg"},
			{URL: "https://example.test/revalidation-back.jpg"},
		},
	})
	require.NoError(t, err)

	var priceSetID, itemID string
	for i, title := range []string{"Small", "Large"} {
		variant, err := productSvc.CreateVariant(ctx, product.ID, productsvc.CreateVariantInput{Title: title})
		require.NoError(t, err)
		set, err := pricingSvc.CreatePriceSet(ctx, []pricingsvc.PriceInput{
			{CurrencyCode: taxedCurrency, Amount: int64(1000 * (i + 1)), MinQuantity: 1},
		})
		require.NoError(t, err)
		require.NoError(t, productSvc.SetVariantPriceSet(ctx, variant.ID, set.ID))
		item, err := inventorySvc.CreateInventoryItem(ctx, inventorysvc.CreateInventoryItemInput{
			SKU: fmt.Sprintf("E2E-REVALIDATION-%d-%d", seq, i), Title: title,
		})
		require.NoError(t, err)
		require.NoError(t, productSvc.SetVariantInventoryItem(ctx, variant.ID, item.ID))
		_, err = inventorySvc.SetInventoryLevel(ctx, item.ID, stockLocationID, 5)
		require.NoError(t, err)
		priceSetID, itemID = set.ID, item.ID
	}

	page := catalogPath(testChannelID, "/products/"+product.Handle)
	listing := catalogPath(testChannelID, "/products?collection_id="+collection.ID)

	// 1. The same page reads the same bytes: a tag that moved between two reads
	// of an unchanged product would never match, and the feature would be dead
	// without one wrong answer to show for it.
	tag := currentTag(t, page)
	for range 4 {
		assert.Equal(t, tag, currentTag(t, page),
			"five reads of an unchanged page must carry one tag; the encoding or the "+
				"enrichment order is not deterministic")
	}
	again := revalidate(t, page, tag)
	require.Equal(t, http.StatusNotModified, again.Code, "body: %s", again.Body.String())
	assert.Empty(t, again.Body.Bytes())
	listed := currentTag(t, listing)
	require.Equal(t, http.StatusNotModified, revalidate(t, listing, listed).Code)

	// 2-4. A write in each module the page reads moves the tag.
	writes := []struct {
		name  string
		write func()
	}{
		{"a tag removed from the vocabulary", func() {
			require.NoError(t, productSvc.DeleteTag(ctx, removed.ID))
		}},
		{"a stock level", func() {
			_, err := inventorySvc.SetInventoryLevel(ctx, itemID, stockLocationID, 7)
			require.NoError(t, err)
		}},
		{"a price", func() {
			_, err := pricingSvc.SetPrices(ctx, priceSetID, []pricingsvc.PriceInput{
				{CurrencyCode: taxedCurrency, Amount: 2500, MinQuantity: 1},
			})
			require.NoError(t, err)
		}},
	}
	for _, step := range writes {
		step.write()

		rec := revalidate(t, page, tag)
		require.Equal(t, http.StatusOK, rec.Code,
			"after %s the page held under the old tag must be sent again; body: %s",
			step.name, rec.Body.String())
		assert.NotEqual(t, tag, rec.Header().Get("ETag"), "after %s the tag must move", step.name)
		tag = rec.Header().Get("ETag")

		// 5. The listing carries the same product, so its tag moves too.
		moved := revalidate(t, listing, listed)
		require.Equal(t, http.StatusOK, moved.Code,
			"after %s the listing held under the old tag must be sent again", step.name)
		listed = moved.Header().Get("ETag")
	}
}
