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

// storeFixture vitrin testlerinin ortak kurulumudur: iki yayında ürün, ikisinin
// de birer varyantı ve varyantlara karşılık gelen sahte fiyat/stok kayıtları.
type storeFixture struct {
	svc      *service.Service
	graph    *fakeGraph
	products []models.Product
}

// newStoreFixture yayında iki ürün kurar ve Query katmanının döneceği kayıtları
// varyant kimliklerine göre hazırlar.
func newStoreFixture(t *testing.T) storeFixture {
	t.Helper()

	graph := &fakeGraph{}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)

	first := seedProduct(t, svc, "tisort", "Tişört")
	second := seedProduct(t, svc, "pantolon", "Pantolon")

	graph.records = []query.Record{
		{
			"id": first.Variants[0].ID,
			"price_set": query.Record{
				"id": "pset_1", "currency_code": "try", "amount": int64(19900),
			},
			"inventory_item": query.Record{"id": "invitem_1", "stocked_quantity": int64(7)},
		},
		{
			"id":        second.Variants[0].ID,
			"price_set": query.Record{"id": "pset_2", "currency_code": "try", "amount": int64(49900)},
		},
	}
	return storeFixture{svc: svc, graph: graph, products: []models.Product{first, second}}
}

// TestListStoreProductsIncludesPriceAndInventory Faz 4 DoD'sinin kalbini
// doğrular: vitrin listesi ürünleri FİYAT ve STOK bilgisiyle döner.
func TestListStoreProductsIncludesPriceAndInventory(t *testing.T) {
	t.Parallel()

	fx := newStoreFixture(t)

	result, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{})
	require.NoError(t, err)
	require.Len(t, result.Items, 2)

	byHandle := map[string]service.StoreProduct{}
	for i := range result.Items {
		byHandle[result.Items[i].Handle] = result.Items[i]
	}

	tisort := byHandle["tisort"]
	require.Len(t, tisort.Variants, 1)
	require.NotNil(t, tisort.Variants[0].PriceSet, "fiyat kümesi varyanta iliştirilmeli")
	assert.Equal(t, int64(19900), tisort.Variants[0].PriceSet["amount"],
		"fiyat tam sayı minor unit olarak taşınmalı")
	require.NotNil(t, tisort.Variants[0].InventoryItem, "stok kalemi varyanta iliştirilmeli")
	assert.Equal(t, int64(7), tisort.Variants[0].InventoryItem["stocked_quantity"])

	pantolon := byHandle["pantolon"]
	require.Len(t, pantolon.Variants, 1)
	assert.Equal(t, "pset_2", pantolon.Variants[0].PriceSet["id"])
	assert.Nil(t, pantolon.Variants[0].InventoryItem,
		"stok bağı olmayan varyantta alan boş kalmalı")
}

// TestListStoreProductsUsesSingleGraphCall zenginleştirmenin TEK bir Query
// çağrısıyla yapıldığını ve spec'in link sözleşmesine uyduğunu doğrular.
//
// Bu test N+1'in yokluğunun kanıtıdır: iki ürün ve iki varyant için Query tam
// olarak bir kez çağrılır.
func TestListStoreProductsUsesSingleGraphCall(t *testing.T) {
	t.Parallel()

	fx := newStoreFixture(t)

	_, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{})
	require.NoError(t, err)

	assert.Equal(t, 1, fx.graph.callCount(), "varyant sayısı ne olursa olsun tek graph çağrısı yapılmalı")

	spec := fx.graph.lastSpec(t)
	assert.Equal(t, service.EntityVariant, spec.Entity,
		"genişletmenin kökü varyanttır; link'ler varyant kimliğiyle kurulur")
	require.Len(t, spec.Expand, 2)
	assert.Equal(t, service.LinkVariantPriceSet, spec.Expand[0].Link)
	assert.Equal(t, "price_set", spec.Expand[0].As)
	assert.Equal(t, service.LinkVariantInventory, spec.Expand[1].Link)
	assert.Equal(t, "inventory_item", spec.Expand[1].As)

	ids, ok := spec.Filters["ids"].([]string)
	require.True(t, ok, "filtre varyant kimliklerini dizge dilimi olarak taşımalı: %#v", spec.Filters)
	assert.Len(t, ids, 2, "her iki ürünün varyantı da tek çağrıda sorulmalı")
}

// TestListStoreProductsHidesUnpublished vitrinin yalnızca yayındaki ürünleri
// gösterdiğini doğrular.
func TestListStoreProductsHidesUnpublished(t *testing.T) {
	t.Parallel()

	graph := &fakeGraph{}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)
	ctx := context.Background()

	_, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "taslak", Title: "Taslak", Status: models.StatusDraft,
	})
	require.NoError(t, err)
	_, err = svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "arsiv", Title: "Arşiv", Status: models.StatusArchived,
	})
	require.NoError(t, err)
	seedProduct(t, svc, "tisort", "Tişört")

	result, err := svc.ListStoreProducts(ctx, service.StoreListOptions{})
	require.NoError(t, err)
	require.Len(t, result.Items, 1, "yalnızca yayındaki ürün listelenmeli")
	assert.Equal(t, "tisort", result.Items[0].Handle)
	assert.Equal(t, 1, result.Count, "count da yalnızca yayındakileri saymalı")
}

// TestGetStoreProductByHandle vitrin ucunun handle ile de çalıştığını doğrular.
func TestGetStoreProductByHandle(t *testing.T) {
	t.Parallel()

	fx := newStoreFixture(t)

	product, err := fx.svc.GetStoreProduct(context.Background(), "tisort", nil)
	require.NoError(t, err)
	assert.Equal(t, "tisort", product.Handle)
	require.Len(t, product.Variants, 1)
	assert.Equal(t, "pset_1", product.Variants[0].PriceSet["id"])
}

// TestGetStoreProductByID vitrin ucunun kimlikle de çalıştığını doğrular.
func TestGetStoreProductByID(t *testing.T) {
	t.Parallel()

	fx := newStoreFixture(t)

	product, err := fx.svc.GetStoreProduct(context.Background(), fx.products[0].ID, nil)
	require.NoError(t, err)
	assert.Equal(t, fx.products[0].ID, product.ID)
}

// TestGetStoreProductHidesDraft yayında olmayan ürünün vitrinde
// BULUNAMADI döndüğünü doğrular.
func TestGetStoreProductHidesDraft(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), &fakeGraph{})
	ctx := context.Background()

	draft, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "taslak", Title: "Taslak", Status: models.StatusDraft,
	})
	require.NoError(t, err)

	_, err = svc.GetStoreProduct(ctx, draft.ID, nil)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "taslak ürün vitrinde bulunamamalı: %v", err)
}

// TestListStoreProductsDegradesWhenProviderMissing pricing/inventory kayıtlı
// olmadığında listelemenin HATA VERMEDİĞİNİ, yalnızca fiyat/stok alanlarının
// boş kaldığını doğrular.
func TestListStoreProductsDegradesWhenProviderMissing(t *testing.T) {
	t.Parallel()

	graph := &fakeGraph{err: errors.NotFound("query_provider_not_found",
		"\"pricing\" entity'si için sorgu sağlayıcısı bulunamadı")}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)
	ctx := context.Background()
	seedProduct(t, svc, "tisort", "Tişört")

	result, err := svc.ListStoreProducts(ctx, service.StoreListOptions{})
	require.NoError(t, err, "eksik modül katalogu düşürmemeli")
	require.Len(t, result.Items, 1)
	require.Len(t, result.Items[0].Variants, 1)
	assert.Nil(t, result.Items[0].Variants[0].PriceSet, "fiyat alanı boş kalmalı")
	assert.Nil(t, result.Items[0].Variants[0].InventoryItem, "stok alanı boş kalmalı")
}

// TestListStoreProductsPropagatesProviderNotFound KAYITLI bir sağlayıcının
// ürettiği NotFound'un yutulmadığını doğrular.
//
// "Sağlayıcı kayıtlı değil" ile "sağlayıcı bulunamadı dedi" aynı SINIFTAN
// (NotFound) hatalardır ama farklı olaylardır: ilki kurulum gerçeğidir, ikincisi
// arızadır. Sınıfa bakan bir düşüş ikincisini de yutar ve vitrin, tek bir log
// satırı dışında hiçbir iz bırakmadan fiyatsız 200 döner.
func TestListStoreProductsPropagatesProviderNotFound(t *testing.T) {
	t.Parallel()

	graph := &fakeGraph{err: errors.NotFound("query_provider_failed",
		"\"price_set\" sağlayıcısının FetchByIDs çağrısı başarısız oldu")}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)
	ctx := context.Background()
	seedProduct(t, svc, "tisort", "Tişört")

	_, err := svc.ListStoreProducts(ctx, service.StoreListOptions{})
	require.Error(t, err, "kayıtlı sağlayıcının hatası sessizce yutulmamalı")
	assert.Equal(t, "product_query_failed", errors.CodeOf(err))
	assert.True(t, errors.IsNotFound(err), "hata sınıfı korunmalı: %v", err)
}

// TestListStoreProductsPropagatesQueryFailure geçici bir Query hatasının
// SESSİZCE YUTULMADIĞINI doğrular.
//
// Ayrım kritiktir: "sağlayıcı kayıtlı değil" bir kurulum gerçeğidir ve katalog
// onsuz da anlamlıdır; "veritabanı erişilemez" ise geçici bir arızadır ve
// fiyatsız bir vitrin sayfası doğru sonuç gibi önbelleğe girmemelidir.
func TestListStoreProductsPropagatesQueryFailure(t *testing.T) {
	t.Parallel()

	graph := &fakeGraph{err: errors.Unavailable("query_link_failed", "link tablosu okunamadı")}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)
	ctx := context.Background()
	seedProduct(t, svc, "tisort", "Tişört")

	_, err := svc.ListStoreProducts(ctx, service.StoreListOptions{})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "hata sınıfı korunmalı: %v", err)
}

// TestListStoreProductsWithoutQueryLayer Query katmanı hiç verilmemişken
// listelemenin çalıştığını doğrular (modül tek başına dağıtılabilir).
func TestListStoreProductsWithoutQueryLayer(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()
	seedProduct(t, svc, "tisort", "Tişört")

	result, err := svc.ListStoreProducts(ctx, service.StoreListOptions{})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Nil(t, result.Items[0].Variants[0].PriceSet)
}

// TestListStoreProductsSkipsGraphWhenNoVariants varyantı olmayan katalogda
// Query katmanına hiç gidilmediğini doğrular.
func TestListStoreProductsSkipsGraphWhenNoVariants(t *testing.T) {
	t.Parallel()

	graph := &fakeGraph{}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)
	ctx := context.Background()

	_, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "tisort", Title: "Tişört", Status: models.StatusPublished,
	})
	require.NoError(t, err)

	result, err := svc.ListStoreProducts(ctx, service.StoreListOptions{})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Empty(t, result.Items[0].Variants)
	assert.Zero(t, graph.callCount(), "genişletilecek varyant yoksa Query'ye gidilmemeli")
}
