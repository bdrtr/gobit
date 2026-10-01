package service_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// fakeStock is inventory's surface in memory: each item is its SKU and title.
type fakeStock struct {
	mu     sync.Mutex
	items  map[string][2]string
	absent error
}

func newFakeStock() *fakeStock { return &fakeStock{items: map[string][2]string{}} }

func (f *fakeStock) Installed(context.Context) error { return f.absent }

func (f *fakeStock) CreateItemForStock(_ context.Context, sku, title string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("iitem_%d", len(f.items)+1)
	f.items[id] = [2]string{sku, title}

	return id, nil
}

// stockService is a product service with inventory's surface bound.
func stockService(t *testing.T, links service.Linker, stock service.VariantStock) *service.Service {
	t.Helper()

	svc, err := service.New(service.Options{Repo: newMemStore(), Links: links, Stock: stock})
	require.NoError(t, err)

	return svc
}

// TestThePanelStocksAVariant is ADR 0310: a variant without an item is given
// one, named by its SKU or by its id when it has none and titled by the
// product and the variant, and linked; a second call returns the same item
// and makes none; a missing variant and a bundle are refused before an item
// is made; and an installation without inventory says so.
func TestThePanelStocksAVariant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	links, stock := newFakeLinker(), newFakeStock()
	svc := stockService(t, links, stock)
	surface := service.NewAdminSurface(svc)
	product, err := surface.CreateProduct(ctx, "Linen Shirt", "")
	require.NoError(t, err)
	named, err := surface.AddVariant(ctx, product, "M", "SH-M")
	require.NoError(t, err)
	unnamed, err := surface.AddVariant(ctx, product, "L", "")
	require.NoError(t, err)

	item, err := surface.StockVariant(ctx, named)
	require.NoError(t, err)
	assert.Equal(t, [2]string{"SH-M", "Linen Shirt — M"}, stock.items[item])
	linked, err := links.List(ctx, service.LinkVariantInventory, named)
	require.NoError(t, err)
	assert.Equal(t, []string{item}, linked)

	again, err := surface.StockVariant(ctx, named)
	require.NoError(t, err)
	assert.Equal(t, item, again, "a variant with an item keeps it")
	assert.Len(t, stock.items, 1, "and no second item is made")

	other, err := surface.StockVariant(ctx, unnamed)
	require.NoError(t, err)
	assert.Equal(t, unnamed, stock.items[other][0], "a variant without a SKU names its item by its id")

	_, err = surface.StockVariant(ctx, "variant_missing")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
	assert.Len(t, stock.items, 2, "no item is made for a variant that does not exist")

	// A bundle is another product's variant made of this one's.
	giftProduct, err := surface.CreateProduct(ctx, "Gift Set", "")
	require.NoError(t, err)
	gift, err := surface.AddVariant(ctx, giftProduct, "Set", "SH-SET")
	require.NoError(t, err)
	current, err := svc.GetProduct(ctx, giftProduct)
	require.NoError(t, err)
	require.NoError(t, surface.SetVariantBundle(ctx, gift, []string{"SH-M"}, []int64{2}, current.Version))
	_, err = surface.StockVariant(ctx, gift)
	require.Error(t, err, "a bundle's stock is its parts'")
	assert.Len(t, stock.items, 2, "and a refused bundle leaves no item behind")

	keeping := service.NewAdminSurface(stockService(t, newFakeLinker(), &fakeStock{
		items: map[string][2]string{}, absent: errors.Unavailable("product_stock_unavailable", "no inventory"),
	}))
	bareProduct, err := keeping.CreateProduct(ctx, "Coffee", "")
	require.NoError(t, err)
	bareVariant, err := keeping.AddVariant(ctx, bareProduct, "Bag", "")
	require.NoError(t, err)
	_, err = keeping.StockVariant(ctx, bareVariant)
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable))
}
