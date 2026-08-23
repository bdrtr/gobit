package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// providerFixture sağlayıcı testlerinin ortak kurulumudur.
type providerFixture struct {
	store    *memStore
	products query.Provider
	variants query.Provider
	seeded   models.Product
}

// newProviderFixture bir ürün ve varyantı olan bir kurulum üretir.
func newProviderFixture(t *testing.T) providerFixture {
	t.Helper()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	seeded := seedProduct(t, svc, "tisort", "Tişört")

	return providerFixture{
		store:    store,
		products: service.NewProductProvider(store),
		variants: service.NewVariantProvider(store),
		seeded:   seeded,
	}
}

// TestProviderEntityNamesMatchRegistration sağlayıcıların sunduğu entity
// adlarının kayıt adlarıyla örtüştüğünü doğrular.
//
// Query, "<entity>.query" adıyla çözdüğü sağlayıcının Entity() değerini
// doğrular; uyuşmazlık sessizce yanlış modülden veri çekmek yerine hata olur.
func TestProviderEntityNamesMatchRegistration(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	assert.Equal(t, "product", fx.products.Entity())
	assert.Equal(t, "variant", fx.variants.Entity())
	assert.Equal(t, service.EntityProduct, fx.products.Entity())
	assert.Equal(t, service.EntityVariant, fx.variants.Entity())
}

// TestProductProviderListReturnsRecords kök listenin kayıt olarak döndüğünü
// ve kimlik alanının bulunduğunu doğrular.
func TestProductProviderListReturnsRecords(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)

	records, err := fx.products.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, fx.seeded.ID, records[0][query.IDField],
		"kayıt, Query'nin birleştirme yaptığı %q alanını taşımalı", query.IDField)
	assert.Equal(t, "tisort", records[0]["handle"])
}

// TestProductProviderFilters desteklenen filtrelerin uygulandığını doğrular.
func TestProductProviderFilters(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	ctx := context.Background()

	records, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"status": "published"},
	})
	require.NoError(t, err)
	assert.Len(t, records, 1)

	records, err = fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"status": "draft"},
	})
	require.NoError(t, err)
	assert.Empty(t, records, "durum filtresi uygulanmalı")

	records, err = fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"handle": "yok"},
	})
	require.NoError(t, err)
	assert.Empty(t, records)
}

// TestProviderRejectsUnknownFilter tanınmayan filtrenin SESSİZCE YOK
// SAYILMADIĞINI doğrular (ADR 0004).
func TestProviderRejectsUnknownFilter(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	ctx := context.Background()

	_, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"renk": "mavi"},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "tanınmayan filtre doğrulama hatası vermeli: %v", err)

	_, err = fx.variants.List(ctx, query.ListOptions{
		Filters: map[string]any{"renk": "mavi"},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "tanınmayan filtre doğrulama hatası vermeli: %v", err)
}

// TestProviderRejectsWrongFilterType filtre değerinin tipinin doğrulandığını
// gösterir.
func TestProviderRejectsWrongFilterType(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)

	_, err := fx.products.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"status": 42},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "tip uyuşmazlığı doğrulama hatası vermeli: %v", err)
}

// TestProviderProjectsFields alan seçiminin uygulandığını ve tanınmayan alanın
// reddedildiğini doğrular.
func TestProviderProjectsFields(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	ctx := context.Background()

	records, err := fx.variants.List(ctx, query.ListOptions{Fields: []string{"id", "title"}})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Len(t, records[0], 2, "yalnızca istenen alanlar dönmeli: %#v", records[0])
	assert.Contains(t, records[0], "id")
	assert.Contains(t, records[0], "title")

	_, err = fx.variants.List(ctx, query.ListOptions{Fields: []string{"fiyat"}})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "tanınmayan alan doğrulama hatası vermeli: %v", err)
}

// TestVariantProviderFetchByIDsIsBatched genişletmenin TEK çağrıyla
// çözüldüğünü ve bulunamayan kimliğin hata olmadığını doğrular (ADR 0004).
func TestVariantProviderFetchByIDsIsBatched(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	variantID := fx.seeded.Variants[0].ID

	before := fx.store.callCount("ListVariantsByIDs")
	records, err := fx.variants.FetchByIDs(context.Background(),
		[]string{variantID, "variant_yok"}, nil)
	require.NoError(t, err, "bulunamayan kimlik hata değildir")
	require.Len(t, records, 1)
	assert.Equal(t, variantID, records[0][query.IDField])
	assert.Equal(t, before+1, fx.store.callCount("ListVariantsByIDs"),
		"kimlik sayısı ne olursa olsun tek sorgu yapılmalı")
}

// TestVariantProviderFiltersByProduct varyantların ürüne göre süzüldüğünü
// doğrular.
func TestVariantProviderFiltersByProduct(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	ctx := context.Background()

	records, err := fx.variants.List(ctx, query.ListOptions{
		Filters: map[string]any{"product_id": fx.seeded.ID},
	})
	require.NoError(t, err)
	assert.Len(t, records, 1)

	records, err = fx.variants.List(ctx, query.ListOptions{
		Filters: map[string]any{"product_ids": []string{"prod_yok"}},
	})
	require.NoError(t, err)
	assert.Empty(t, records)
}

// TestVariantProviderIDsFilterAcceptsBothShapes kimlik filtresinin hem tek
// dizge hem dilim biçimini kabul ettiğini doğrular.
func TestVariantProviderIDsFilterAcceptsBothShapes(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	ctx := context.Background()
	variantID := fx.seeded.Variants[0].ID

	single, err := fx.variants.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": variantID},
	})
	require.NoError(t, err)
	require.Len(t, single, 1)

	many, err := fx.variants.List(ctx, query.ListOptions{
		Filters: map[string]any{"ids": []any{variantID}},
	})
	require.NoError(t, err)
	require.Len(t, many, 1)
	assert.Equal(t, single[0][query.IDField], many[0][query.IDField])
}

// TestProviderPaging limit ve offset'in uygulandığını doğrular.
func TestProviderPaging(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	seedProduct(t, svc, "bir", "Bir")
	seedProduct(t, svc, "iki", "İki")
	seedProduct(t, svc, "uc", "Üç")
	products := service.NewProductProvider(store)
	ctx := context.Background()

	all, err := products.List(ctx, query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, all, 3, "limit verilmezse sınırsız sayılmalı")

	page, err := products.List(ctx, query.ListOptions{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, page, 2)

	rest, err := products.List(ctx, query.ListOptions{Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.Len(t, rest, 1)
}
