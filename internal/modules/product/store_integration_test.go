//go:build integration

package product_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Bu dosya Faz 4'ün Definition of Done'ını uçtan uca kanıtlar:
//
//	Admin API'den ürün + varyant oluşturulabiliyor, varyanta fiyat ve stok
//	BAĞLANABİLİYOR, Store API ürünleri fiyat ve stok bilgisiyle listeliyor.
//
// Kurulum gerçek çekirdeği kullanır: gerçek link servisi (gerçek link
// tablolarıyla), gerçek Query katmanı ve gerçek container. pricing ve inventory
// modülleri BURADA YOKTUR ve import EDİLEMEZ (Prensip 2.4); yerlerine yalnızca
// query.Provider sözleşmesini karşılayan sahte sağlayıcılar container'a
// "pricing.query" ve "inventory.query" adlarıyla konur — gerçek modüller de
// çekirdeğe tam olarak böyle görünür.

// stubProvider başka bir modülün Query sağlayıcısını taklit eder.
type stubProvider struct {
	entity string

	mu         sync.Mutex
	records    map[string]query.Record
	fetchCalls int
	fetchedIDs []string
}

var _ query.Provider = (*stubProvider)(nil)

// newStubProvider verilen entity için sahte sağlayıcı üretir.
func newStubProvider(entity string) *stubProvider {
	return &stubProvider{entity: entity, records: map[string]query.Record{}}
}

// put sağlayıcının döneceği kaydı ekler.
func (s *stubProvider) put(id string, rec query.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec["id"] = id
	s.records[id] = rec
}

// Entity sağlayıcının entity adını döner.
func (s *stubProvider) Entity() string { return s.entity }

// List kök listeleme yüzeyidir; bu testlerde kök hep varyanttır, bu yüzden
// çağrılması beklenmez.
func (s *stubProvider) List(_ context.Context, _ query.ListOptions) ([]query.Record, error) {
	return nil, nil
}

// FetchByIDs verilen kimliklerin kayıtlarını döner ve çağrıyı sayar.
func (s *stubProvider) FetchByIDs(_ context.Context, ids, _ []string) ([]query.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.fetchCalls++
	s.fetchedIDs = append(s.fetchedIDs, ids...)

	out := make([]query.Record, 0, len(ids))
	for _, id := range ids {
		if rec, ok := s.records[id]; ok {
			out = append(out, rec)
		}
	}
	return out, nil
}

// calls FetchByIDs'in kaç kez çağrıldığını döner.
func (s *stubProvider) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fetchCalls
}

// system uçtan uca kurulmuş bir sistemdir.
type system struct {
	router    chi.Router
	links     link.LinkService
	container *container.Container
	pricing   *stubProvider
	inventory *stubProvider
}

// Diğer iki modülün Query katmanındaki ENTITY adları. Bunlar modül adları
// DEĞİLDİR ve gerçek modüllerin kaydettiği adlarla birebir aynı olmalıdır.
const (
	entityPriceSet      = "price_set"
	entityInventoryItem = "inventory_item"
)

// newSystem çekirdeği ve product modülünü gerçekten ayağa kaldırır.
//
// Akış main.go ile aynıdır: havuz, link servisi ve Query container'a konur,
// sonra modül Register edilir (servisini, sağlayıcılarını ve link tanımlarını
// bildirir), en son route'lar bağlanır.
func newSystem(t *testing.T) system {
	t.Helper()
	ctx := context.Background()

	c := container.New(nil)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	links := link.New(testPool, nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", links))
	require.NoError(t, c.Provide("core.query", query.New(links, c, nil)))
	// Olay veri yolu ZORUNLUDUR: Register onu çözemezse açılış düşer
	// (bkz. [TestRegisterOlayVeriYoluOlmadanDuser]). Kurulum main.go'nunkiyle
	// aynı sırayı izler; veri yolu modüller ayağa kalkmadan önce konur.
	require.NoError(t, c.Provide("core.eventbus", eventbus.NewInMemory(nil)))

	// pricing ve inventory modülleri bu ikili ile temsil edilir; çekirdek
	// onları da yalnızca bu adlar ve bu arayüz üzerinden tanır.
	//
	// Kayıt adları MODÜL adı değil ENTITY adıdır: bir modül birden çok entity
	// sunabilir (product modülü "product" ve "variant" kaydeder), bu yüzden
	// Query hedefi link.LinkSide.Entity üzerinden çözer. Gerçek modüller de
	// tam olarak bu adları kullanır (pricing -> "price_set",
	// inventory -> "inventory_item"); burada modül adı yazmak, gerçek sistemde
	// var olmayan bir kurulumu sınamak olurdu.
	pricing := newStubProvider(entityPriceSet)
	inventory := newStubProvider(entityInventoryItem)
	require.NoError(t, c.Provide(entityPriceSet+query.ProviderSuffix, pricing))
	require.NoError(t, c.Provide(entityInventoryItem+query.ProviderSuffix, inventory))

	mod := product.New(product.Options{})
	require.NoError(t, mod.Register(ctx, c))

	router := chi.NewRouter()
	mod.Routes(router)

	return system{router: router, links: links, container: c, pricing: pricing, inventory: inventory}
}

// request istek yapar ve yanıtı döner.
//
// İstek TAM YETKİLİ bir kimlik taşır. Üretimde kimliği corehttp.RequireAdmin
// context'e koyar; bu kurulum router'ı doğrudan bağladığı için o middleware
// devrede değildir ve kimlik elle konur. Gerekçesi, yönetim uçlarına
// corehttp.RequireScope eklenmesidir: kimliksiz bir istek artık handler'a hiç
// ulaşmadan 401 alır ve buradaki testler Faz 4 DoD'si yerine yetki katmanını
// ölçerdi. Yetkinin KENDİSİ api/yetki_test.go'da sınanır; bu dosyanın
// iddiaları değişmedi.
func (s system) request(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID:     "usr_test",
		Kind:   "user",
		Scopes: []string{corehttp.ScopeAdmin},
	}))
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}

// jsonBody yanıt gövdesini haritaya çözer.
func jsonBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "gövde: %s", rec.Body.String())
	return out
}

// jsonAlan bir JSON nesnesindeki alanı BEKLENEN tipte döner.
//
// Denetlenmemiş bir tip iddiası ("nesne[alan].(string)") aynı işi bir satırda
// yapardı ama arıza anında paniğe düşer ve testin çıktısı yalnızca bir yığın
// izi olurdu. Burada beklenen tip ile GELEN değer birlikte yazılır; yanıt şeması
// kaydığında hangi alanın ne olduğu tek satırda görünür.
func jsonAlan[T any](t *testing.T, nesne map[string]any, alan string) T {
	t.Helper()

	deger, ok := nesne[alan].(T)
	require.True(t, ok, "%q alanı %T tipinde olmalı; gelen: %#v", alan, deger, nesne[alan])
	return deger
}

// jsonNesne bir JSON değerini nesne olarak döner.
//
// [jsonAlan] alan adıyla çalışır; bu, dizideki bir ÖĞE için gerekir ve öğenin
// adı olmadığı için mesaja değerin kendisi yazılır.
func jsonNesne(t *testing.T, deger any) map[string]any {
	t.Helper()

	nesne, ok := deger.(map[string]any)
	require.True(t, ok, "değer nesne olmalı; gelen: %#v", deger)
	return nesne
}

// TestModuleRegisterWiresContainer Register'ın sözleşmedeki dört şeyi de
// yaptığını doğrular: servis kaydı, interop yüzeyi, Query sağlayıcıları ve link
// tanımları.
func TestModuleRegisterWiresContainer(t *testing.T) {
	ctx := context.Background()
	sys := newSystem(t)

	svc, err := container.Resolve[*service.Service](sys.container, product.ServiceName)
	require.NoError(t, err, "servis %q adıyla çözülebilmeli", product.ServiceName)
	assert.NotNil(t, svc)

	// Modüller arası yüzey servisten AYRI bir adla kayıtlıdır: eklentiler
	// katalogu yalnızca bu adla ve ilkel tiplerle görür (ADR 0006).
	interop, err := container.Resolve[*service.Interop](sys.container, product.InteropName)
	require.NoError(t, err, "yüzey %q adıyla çözülebilmeli", product.InteropName)
	assert.NotNil(t, interop)

	for _, entity := range []string{service.EntityProduct, service.EntityVariant} {
		name := entity + query.ProviderSuffix
		provider, err := container.Resolve[query.Provider](sys.container, name)
		require.NoError(t, err, "%q sağlayıcısı kayıtlı olmalı", name)
		assert.Equal(t, entity, provider.Entity(),
			"sağlayıcının entity adı kayıt adıyla örtüşmeli (Query bunu doğrular)")
	}

	for _, want := range service.Definitions() {
		got, err := sys.links.Definition(ctx, want.Name)
		require.NoError(t, err, "%q link tanımı bildirilmiş olmalı", want.Name)
		assert.Equal(t, want, got, "bildirilen tanım sözleşmeyle birebir aynı olmalı")
	}
}

// TestAdminAPICreatesProductAndVariant yönetim API'sinden ürün ve varyant
// oluşturulabildiğini doğrular (Faz 4 DoD).
func TestAdminAPICreatesProductAndVariant(t *testing.T) {
	sys := newSystem(t)
	handle := uniqueHandle("admin-urun")

	rec := sys.request(t, http.MethodPost, "/admin/v1/products", `{
		"handle": "`+handle+`",
		"title": "Yönetimden Ürün",
		"status": "published",
		"options": [{"title": "Beden", "values": ["S", "M"]}],
		"variants": [{"title": "S beden", "options": {"Beden": "S"}}]
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	created := itemData(t, rec)
	productID := jsonAlan[string](t, created, "id")
	assert.True(t, strings.HasPrefix(productID, "prod_"))
	require.Len(t, jsonAlan[[]any](t, created, "variants"), 1)

	rec = sys.request(t, http.MethodPost, "/admin/v1/products/"+productID+"/variants", `{
		"title": "M beden",
		"options": {"Beden": "M"}
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	variant := itemData(t, rec)
	assert.True(t, strings.HasPrefix(jsonAlan[string](t, variant, "id"), "variant_"))
	optionValues := jsonAlan[[]any](t, variant, "option_values")
	require.Len(t, optionValues, 1, "varyant seçenek değerine bağlanmalı")
	ilkDeger, ok := optionValues[0].(map[string]any)
	require.True(t, ok, "seçenek değeri nesne olmalı; gelen: %#v", optionValues[0])
	assert.Equal(t, "M", ilkDeger["value"])

	rec = sys.request(t, http.MethodGet, "/admin/v1/products/"+productID, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, jsonAlan[[]any](t, itemData(t, rec), "variants"), 2,
		"ikinci varyant da ürüne bağlanmalı")

	// Aynı handle ikinci kez kullanılamaz; hata 409 olmalıdır.
	rec = sys.request(t, http.MethodPost, "/admin/v1/products",
		`{"handle": "`+handle+`", "title": "Kopya"}`)
	assert.Equal(t, http.StatusConflict, rec.Code, "gövde: %s", rec.Body.String())
}

// TestStoreListingReturnsPriceAndStock FAZ 4'ÜN KALBİDİR: vitrin listesi
// ürünleri fiyat ve stok bilgisiyle döner.
//
// Fiyat pricing, stok inventory modülünün verisidir; product modülü ikisini de
// import etmez. Veri, admin akışında kurulan GERÇEK link satırları üzerinden,
// GERÇEK Query katmanıyla toplanır.
func TestStoreListingReturnsPriceAndStock(t *testing.T) {
	sys := newSystem(t)
	ctx := context.Background()

	svc, err := container.Resolve[*service.Service](sys.container, product.ServiceName)
	require.NoError(t, err)

	// Vitrin tüm yayındaki ürünleri listeler; bu test kendi kümesini bir
	// koleksiyonla ayırır ki diğer testlerin bıraktığı kayıtlar sonucu
	// bulandırmasın.
	collection, err := svc.CreateCollection(ctx, service.CreateCollectionInput{
		Title: "Vitrin " + uniqueHandle("koleksiyon"),
	})
	require.NoError(t, err)

	handle := uniqueHandle("vitrin-urun")
	rec := sys.request(t, http.MethodPost, "/admin/v1/products", `{
		"handle": "`+handle+`",
		"title": "Vitrin Ürünü",
		"status": "published",
		"collection_id": "`+collection.ID+`",
		"variants": [{"title": "Tek beden"}, {"title": "Çift beden"}]
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	created := itemData(t, rec)
	productID := jsonAlan[string](t, created, "id")
	variants := jsonAlan[[]any](t, created, "variants")
	require.Len(t, variants, 2)
	firstVariantID := jsonAlan[string](t, jsonNesne(t, variants[0]), "id")
	secondVariantID := jsonAlan[string](t, jsonNesne(t, variants[1]), "id")

	// pricing ve inventory modüllerinin ürettiği kayıtlar.
	sys.pricing.put("pset_"+firstVariantID, query.Record{"currency_code": "try", "amount": int64(19900)})
	sys.pricing.put("pset_"+secondVariantID, query.Record{"currency_code": "try", "amount": int64(24900)})
	sys.inventory.put("invitem_"+firstVariantID, query.Record{"stocked_quantity": int64(12)})

	// Bağlar admin akışında kurulur.
	for _, variantID := range []string{firstVariantID, secondVariantID} {
		rec = sys.request(t, http.MethodPut, "/admin/v1/variants/"+variantID+"/price-set",
			`{"price_set_id": "pset_`+variantID+`"}`)
		require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	}
	rec = sys.request(t, http.MethodPut, "/admin/v1/variants/"+firstVariantID+"/inventory-item",
		`{"inventory_item_id": "invitem_`+firstVariantID+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())

	links := itemData(t, rec)
	assert.Equal(t, "invitem_"+firstVariantID, links["inventory_item_id"])
	assert.Equal(t, "pset_"+firstVariantID, links["price_set_id"],
		"iki bağ birbirini ezmemeli")

	pricingCallsBefore := sys.pricing.calls()
	inventoryCallsBefore := sys.inventory.calls()

	// --- Vitrin listesi ---
	rec = sys.request(t, http.MethodGet, "/store/v1/products?collection_id="+collection.ID, "")
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())

	body := jsonBody(t, rec)
	assert.Equal(t, float64(1), body["count"])
	data := jsonAlan[[]any](t, body, "data")
	require.Len(t, data, 1)

	storeProduct := jsonNesne(t, data[0])
	assert.Equal(t, handle, storeProduct["handle"])
	storeVariants := jsonAlan[[]any](t, storeProduct, "variants")
	require.Len(t, storeVariants, 2)

	byID := map[string]map[string]any{}
	for _, raw := range storeVariants {
		variant := jsonNesne(t, raw)
		byID[jsonAlan[string](t, variant, "id")] = variant
	}

	first := byID[firstVariantID]
	require.NotNil(t, first)
	priceSet, ok := first["price_set"].(map[string]any)
	require.True(t, ok, "varyant fiyat kümesini taşımalı: %#v", first)
	assert.Equal(t, "pset_"+firstVariantID, priceSet["id"])
	assert.Equal(t, float64(19900), priceSet["amount"], "fiyat minor unit tam sayıdır")
	inventoryItem, ok := first["inventory_item"].(map[string]any)
	require.True(t, ok, "varyant stok kalemini taşımalı: %#v", first)
	assert.Equal(t, float64(12), inventoryItem["stocked_quantity"])

	second := byID[secondVariantID]
	require.NotNil(t, second)
	assert.Equal(t, "pset_"+secondVariantID, jsonAlan[map[string]any](t, second, "price_set")["id"])
	assert.NotContains(t, second, "inventory_item",
		"stok bağı olmayan varyantta alan hiç yazılmamalı")

	// N+1 yok: iki varyant için hedef modüllerin her birine TEK çağrı yapılır.
	assert.Equal(t, pricingCallsBefore+1, sys.pricing.calls(),
		"fiyat sağlayıcısına varyant başına değil, genişletme başına tek çağrı yapılmalı")
	assert.Equal(t, inventoryCallsBefore+1, sys.inventory.calls(),
		"stok sağlayıcısına varyant başına değil, genişletme başına tek çağrı yapılmalı")

	// --- Vitrin tekil ucu ---
	rec = sys.request(t, http.MethodGet, "/store/v1/products/"+handle, "")
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	single := itemData(t, rec)
	assert.Equal(t, productID, single["id"])
	require.Len(t, jsonAlan[[]any](t, single, "variants"), 2)
}

// TestStoreListingHidesDraftProducts vitrinin taslak ürünü göstermediğini
// gerçek veritabanında doğrular.
func TestStoreListingHidesDraftProducts(t *testing.T) {
	sys := newSystem(t)
	ctx := context.Background()

	svc, err := container.Resolve[*service.Service](sys.container, product.ServiceName)
	require.NoError(t, err)
	collection, err := svc.CreateCollection(ctx, service.CreateCollectionInput{
		Title: "Taslak " + uniqueHandle("koleksiyon"),
	})
	require.NoError(t, err)

	handle := uniqueHandle("taslak-urun")
	rec := sys.request(t, http.MethodPost, "/admin/v1/products", `{
		"handle": "`+handle+`",
		"title": "Taslak Ürün",
		"collection_id": "`+collection.ID+`",
		"variants": [{"title": "Tek"}]
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	rec = sys.request(t, http.MethodGet, "/store/v1/products?collection_id="+collection.ID, "")
	require.Equal(t, http.StatusOK, rec.Code)
	body := jsonBody(t, rec)
	assert.Zero(t, body["count"], "taslak ürün vitrinde sayılmamalı")
	assert.Empty(t, jsonAlan[[]any](t, body, "data"), "taslak ürün vitrinde listelenmemeli")

	rec = sys.request(t, http.MethodGet, "/store/v1/products/"+handle, "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "taslak ürün vitrinde bulunamamalı")
}

// TestStoreListingWithCountFalseKeepsPageDropsCount sayacın kapatılmasının
// GERÇEK veritabanında sayfayı değiştirmediğini ve alanı gövdeden düşürdüğünü
// doğrular.
//
// Birim testleri kararın servise ULAŞTIĞINI ve sayaç sorgusunun hiç
// çalışmadığını kanıtlar; bu test kalan tek soruyu cevaplar: aynı süzgeci
// paylaşan liste sorgusu, sayaç ortadan kalkınca da AYNI satırları döndürüyor
// mu. İkisi tek bir SQL şablonundan geçtiği için (bkz.
// repository/saleschannel.go) bunun gerçek planla sınanması gerekir.
//
// Ölçüm bağlamı: 52.004 ürünlük gobit_load kurulumunda sayaç isteğin SQL'inin
// %99'udur (67,00 ms → 0,65 ms). Buradaki katalog küçüktür, yani test SÜREYİ
// değil DAVRANIŞI sabitler — süreyi bir teste iddia ettirmek, testi makinenin
// yüküne bağlamak olurdu.
func TestStoreListingWithCountFalseKeepsPageDropsCount(t *testing.T) {
	sys := newSystem(t)
	ctx := context.Background()

	svc, err := container.Resolve[*service.Service](sys.container, product.ServiceName)
	require.NoError(t, err)

	collection, err := svc.CreateCollection(ctx, service.CreateCollectionInput{
		Title: "Sayaç " + uniqueHandle("koleksiyon"),
	})
	require.NoError(t, err)

	for i := range 3 {
		rec := sys.request(t, http.MethodPost, "/admin/v1/products", `{
			"handle": "`+uniqueHandle(fmt.Sprintf("sayac-%d", i))+`",
			"title": "Sayaç Ürünü",
			"status": "published",
			"collection_id": "`+collection.ID+`",
			"variants": [{"title": "Tek"}]
		}`)
		require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())
	}

	yol := "/store/v1/products?limit=2&collection_id=" + collection.ID

	rec := sys.request(t, http.MethodGet, yol, "")
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())

	sayarak := jsonBody(t, rec)
	assert.InDelta(t, float64(3), sayarak["count"], 0,
		"varsayılan sayaç süzülmüş kümenin TAMAMINI saymalı (sayfa 2 kayıt)")

	rec = sys.request(t, http.MethodGet, yol+"&with_count=false", "")
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())

	assert.NotContains(t, rec.Body.String(), `"count"`,
		"sayaç kapalıyken alan gövdede HİÇ olmamalı: %s", rec.Body.String())

	saymadan := jsonBody(t, rec)
	assert.Equal(t, jsonAlan[[]any](t, sayarak, "data"), jsonAlan[[]any](t, saymadan, "data"),
		"sayacı kapatmak sayfanın İÇERİĞİNİ değiştirmemeli")
	assert.InDelta(t, sayarak["limit"], saymadan["limit"], 0)
	assert.InDelta(t, sayarak["offset"], saymadan["offset"], 0)
}

// TestVariantDeletionRemovesLinks silinen varyantın GERÇEK link tablosundaki
// bağlarının temizlendiğini doğrular.
//
// Temizlenmeseydi pricing tarafından ters yönde yapılan bir sorgu var olmayan
// bir varyanta çıkardı.
func TestVariantDeletionRemovesLinks(t *testing.T) {
	sys := newSystem(t)
	ctx := context.Background()

	rec := sys.request(t, http.MethodPost, "/admin/v1/products", `{
		"handle": "`+uniqueHandle("silme-bag")+`",
		"title": "Bağlı Ürün",
		"status": "published",
		"variants": [{"title": "Tek"}]
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	created := itemData(t, rec)
	variantID := jsonAlan[string](t, jsonNesne(t, jsonAlan[[]any](t, created, "variants")[0]), "id")

	rec = sys.request(t, http.MethodPut, "/admin/v1/variants/"+variantID+"/price-set",
		`{"price_set_id": "pset_silinecek"}`)
	require.Equal(t, http.StatusOK, rec.Code)

	linked, err := sys.links.List(ctx, service.LinkVariantPriceSet, variantID)
	require.NoError(t, err)
	require.Equal(t, []string{"pset_silinecek"}, linked, "bağ gerçekten kurulmalı")

	rec = sys.request(t, http.MethodDelete, "/admin/v1/variants/"+variantID, "")
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())

	linked, err = sys.links.List(ctx, service.LinkVariantPriceSet, variantID)
	require.NoError(t, err)
	assert.Empty(t, linked, "silinen varyantın bağı kalmamalı")
}

// TestPriceSetLinkIsReplacedNotDuplicated fiyat kümesi değiştirildiğinde
// kardinalitenin (OneToOne) korunduğunu gerçek link tablosunda doğrular.
func TestPriceSetLinkIsReplacedNotDuplicated(t *testing.T) {
	sys := newSystem(t)
	ctx := context.Background()

	rec := sys.request(t, http.MethodPost, "/admin/v1/products", `{
		"handle": "`+uniqueHandle("bag-degistir")+`",
		"title": "Fiyatı Değişen",
		"status": "published",
		"variants": [{"title": "Tek"}]
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	created := itemData(t, rec)
	variantID := jsonAlan[string](t, jsonNesne(t, jsonAlan[[]any](t, created, "variants")[0]), "id")

	for _, priceSetID := range []string{"pset_eski", "pset_yeni"} {
		rec = sys.request(t, http.MethodPut, "/admin/v1/variants/"+variantID+"/price-set",
			`{"price_set_id": "`+priceSetID+`"}`)
		require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	}

	linked, err := sys.links.List(ctx, service.LinkVariantPriceSet, variantID)
	require.NoError(t, err)
	assert.Equal(t, []string{"pset_yeni"}, linked,
		"OneToOne bağ değiştirilmeli, ikinci bir satır eklenmemeli")
}

// TestPriceSetLinkKeepsExistingWhenTargetIsTaken çakışmayla düşen bir yeniden
// bağlamanın varyantın MEVCUT bağını bozmadığını GERÇEK link servisiyle
// doğrular.
//
// Sahte bir linker bunu kanıtlayamaz: kısıtı zorlayan şey, OneToOne'ın her iki
// ucuna kurulan benzersiz indekstir. Test iki senaryoyu birlikte kilitler:
//
//   - başka bir varyanta bağlı hedefi istemek 409 döner ve HİÇBİR ŞEY değişmez
//     (aksi hâlde varyant fiyatsız kalır ve vitrin onu öyle yayınlardı);
//   - aynı varyantı SERBEST yeni bir hedefe taşımak çalışmaya devam eder
//     (FROM ucu da benzersiz olduğu için eski bağın kaldırılması şarttır).
func TestPriceSetLinkKeepsExistingWhenTargetIsTaken(t *testing.T) {
	sys := newSystem(t)
	ctx := context.Background()

	rec := sys.request(t, http.MethodPost, "/admin/v1/products", `{
		"handle": "`+uniqueHandle("bag-cakisma")+`",
		"title": "Çakışan Bağ",
		"status": "published",
		"variants": [{"title": "Birinci"}, {"title": "İkinci"}]
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	created := itemData(t, rec)
	variants := jsonAlan[[]any](t, created, "variants")
	require.Len(t, variants, 2)
	firstVariantID := jsonAlan[string](t, jsonNesne(t, variants[0]), "id")
	secondVariantID := jsonAlan[string](t, jsonNesne(t, variants[1]), "id")

	// Kimlikler test başına benzersiz olmalı: OneToOne'ın TO ucu link tablosunun
	// TAMAMINDA benzersizdir, sabit bir kimlik başka bir testin bağıyla çakışırdı.
	owned := "pset_" + uniqueHandle("sahipli")
	held := "pset_" + uniqueHandle("mevcut")
	free := "pset_" + uniqueHandle("serbest")

	rec = sys.request(t, http.MethodPut, "/admin/v1/variants/"+firstVariantID+"/price-set",
		`{"price_set_id": "`+owned+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	rec = sys.request(t, http.MethodPut, "/admin/v1/variants/"+secondVariantID+"/price-set",
		`{"price_set_id": "`+held+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())

	// owned zaten birinci varyanta bağlı; ikinci varyant onu isteyemez.
	rec = sys.request(t, http.MethodPut, "/admin/v1/variants/"+secondVariantID+"/price-set",
		`{"price_set_id": "`+owned+`"}`)
	require.Equal(t, http.StatusConflict, rec.Code, "gövde: %s", rec.Body.String())

	linked, err := sys.links.List(ctx, service.LinkVariantPriceSet, secondVariantID)
	require.NoError(t, err)
	assert.Equal(t, []string{held}, linked,
		"409 dönen istek varyantın mevcut fiyat bağını bozmamalı")

	linked, err = sys.links.List(ctx, service.LinkVariantPriceSet, firstVariantID)
	require.NoError(t, err)
	assert.Equal(t, []string{owned}, linked, "hedefin asıl sahibi de etkilenmemeli")

	// Serbest bir hedefe taşımak hâlâ çalışmalı.
	rec = sys.request(t, http.MethodPut, "/admin/v1/variants/"+secondVariantID+"/price-set",
		`{"price_set_id": "`+free+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())

	linked, err = sys.links.List(ctx, service.LinkVariantPriceSet, secondVariantID)
	require.NoError(t, err)
	assert.Equal(t, []string{free}, linked, "serbest hedefe taşıma eski bağı değiştirmeli")
}

// TestPriceSetLinkRejectsUnknownVariant var olmayan varyanta bağ kurulmasının
// 404 döndüğünü doğrular.
func TestPriceSetLinkRejectsUnknownVariant(t *testing.T) {
	sys := newSystem(t)

	rec := sys.request(t, http.MethodPut, "/admin/v1/variants/variant_yok/price-set",
		`{"price_set_id": "pset_1"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code, "gövde: %s", rec.Body.String())
}

// TestStoreListingDegradesWithoutOtherModules product'ın TEK BAŞINA
// dağıtıldığında vitrinin çalışmaya devam ettiğini kanıtlar.
//
// Faz 4'ün modülerlik garantisi budur: pricing ve inventory kayıtlı değilse
// vitrin 500 DEĞİL, fiyatsız/stoksuz 200 döner. Bu davranış, Query'nin
// "sağlayıcı yok" hatasını ayırt etmeye dayanır ve product o hatayı ÇEKİRDEKTEN
// KOPYALANMIŞ bir dize sabitiyle tanır (service.codeProviderNotFound).
//
// Bu test iki dizeyi birbirine BAĞLAR: çekirdek sabiti yeniden adlandırırsa
// burada düşer. Aksi hâlde çekirdekteki bir yeniden adlandırma hiçbir kapıyı
// düşürmeden bu garantiyi sessizce kırardı.
func TestStoreListingDegradesWithoutOtherModules(t *testing.T) {
	ctx := context.Background()

	c := container.New(nil)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	links := link.New(testPool, nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", links))
	require.NoError(t, c.Provide("core.query", query.New(links, c, nil)))
	require.NoError(t, c.Provide("core.eventbus", eventbus.NewInMemory(nil)))
	// pricing ve inventory sağlayıcıları BİLİNÇLİ olarak kaydedilmez.

	mod := product.New(product.Options{})
	require.NoError(t, mod.Register(ctx, c))

	router := chi.NewRouter()
	mod.Routes(router)

	svc, err := container.Resolve[*service.Service](c, product.ServiceName)
	require.NoError(t, err)

	prod, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title:  "Yalnız modül ürünü",
		Status: productmodels.StatusPublished,
	})
	require.NoError(t, err)
	_, err = svc.CreateVariant(ctx, prod.ID, service.CreateVariantInput{Title: "Tek beden"})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/store/v1/products", http.NoBody)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code,
		"pricing/inventory kayıtlı değilken vitrin 500 DEĞİL 200 dönmeli: %s", rec.Body.String())

	var body struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	// Havuz testler arasında paylaşıldığı için kendi ürünümüz aranır;
	// listenin uzunluğuna dayanmak testi komşu testlere bağımlı kılardı.
	var mine map[string]any
	for _, rec := range body.Data {
		if rec["id"] == prod.ID {
			mine = rec
			break
		}
	}
	require.NotNil(t, mine, "oluşturulan ürün vitrinde dönmeli")

	variants, ok := mine["variants"].([]any)
	require.True(t, ok, "varyantlar dönmeli: %v", mine)
	require.Len(t, variants, 1)

	variant, ok := variants[0].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, variant, "price_set", "sağlayıcı yokken fiyat alanı hiç olmamalı")
	assert.NotContains(t, variant, "inventory_item", "sağlayıcı yokken stok alanı hiç olmamalı")
}
