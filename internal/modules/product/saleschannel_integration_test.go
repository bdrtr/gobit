//go:build integration

package product_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Bu dosya vitrinin SATIŞ KANALI süzgecini GERÇEK veritabanında kanıtlar.
//
// Buradaki iddiaların hiçbiri sahte bir depoyla kanıtlanamaz: süzgeç ürünün
// kendi sorgusuna eklenen bir EXISTS/NOT EXISTS koşuludur, koşulun sorguladığı
// link tablosunu core/link çalışma anında kurar ve LIMIT/OFFSET ile toplam
// sayacın süzülmüş küme üzerinde çalıştığı ancak gerçek SQL'de görülür.
//
// auth modülü BURADA YOKTUR ve import EDİLEMEZ (Prensip 2.4): satış kanalı
// kimlikleri, tıpkı üretimde publishable anahtardan gelecekleri gibi, düz
// dizgelerdir. product'ın auth hakkında bildiği tek şey link adı ve entity
// adıdır.

// storeChannelRequest verilen satış kanallarına bağlı bir mağaza isteği yapar.
//
// Üretimde kimliği corehttp.RequireStore koyar (publishable anahtarın
// kanallarıyla); bu kurulum router'ı doğrudan bağladığı için kimlik elle
// konur. channels nil ise kimlik yine KONUR ama kanalsızdır — nil dilim ile
// "kimlik yok" durumunu ayırmak için ayrı bir yardımcı vardır
// (bkz. [system.storeRequestWithoutPrincipal]).
func (s system) storeChannelRequest(t *testing.T, target string, channels []string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID:              "apk_test",
		Kind:            "api_key",
		SalesChannelIDs: channels,
	}))
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}

// storeRequestWithoutPrincipal kimliksiz bir mağaza isteği yapar.
func (s system) storeRequestWithoutPrincipal(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}

// storeListing vitrin listesinden dönen handle'ları ve toplam sayacı verir.
func storeListing(t *testing.T, rec *httptest.ResponseRecorder) (handles []string, count int) {
	t.Helper()

	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	body := jsonBody(t, rec)

	countValue, ok := body["count"].(float64)
	require.True(t, ok, "count sayı olmalı: %#v", body["count"])

	data, ok := body["data"].([]any)
	require.True(t, ok, "data dizi olmalı: %#v", body["data"])
	for _, raw := range data {
		item, ok := raw.(map[string]any)
		require.True(t, ok, "liste öğesi nesne olmalı: %#v", raw)
		handle, ok := item["handle"].(string)
		require.True(t, ok, "ürün handle taşımalı: %#v", item)
		handles = append(handles, handle)
	}
	return handles, int(countValue)
}

// itemData tekil yanıt zarfındaki "data" nesnesini verir.
func itemData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	data, ok := jsonBody(t, rec)["data"].(map[string]any)
	require.True(t, ok, "data nesne olmalı: %s", rec.Body.String())
	return data
}

// channelFixture kanal testlerinin ortak kurulumudur.
//
// Testler tek bir veritabanını paylaştığı için her test kendi ürünlerini bir
// KOLEKSİYONLA ayırır; aksi hâlde komşu testlerin bıraktığı yayındaki ürünler
// listeye karışır ve sayaç iddiaları anlamsızlaşır. Kanal kimlikleri de test
// başına benzersizdir.
type channelFixture struct {
	sys          system
	collectionID string
	channelA     string
	channelB     string
}

// newChannelFixture izole bir koleksiyon ve iki kanal kimliği üretir.
func newChannelFixture(t *testing.T) channelFixture {
	t.Helper()

	sys := newSystem(t)
	svc, err := container.Resolve[*service.Service](sys.container, product.ServiceName)
	require.NoError(t, err)

	collection, err := svc.CreateCollection(context.Background(), service.CreateCollectionInput{
		Title: "Kanal " + uniqueHandle("koleksiyon"),
	})
	require.NoError(t, err)

	return channelFixture{
		sys:          sys,
		collectionID: collection.ID,
		channelA:     "sc_" + uniqueHandle("a"),
		channelB:     "sc_" + uniqueHandle("b"),
	}
}

// seedPublished koleksiyona yayında bir ürün ekler ve kimliğini döner.
func (f channelFixture) seedPublished(t *testing.T, handle string) string {
	t.Helper()

	rec := f.sys.request(t, http.MethodPost, "/admin/v1/products", `{
		"handle": "`+handle+`",
		"title": "Kanal Ürünü",
		"status": "published",
		"collection_id": "`+f.collectionID+`"
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	id, ok := itemData(t, rec)["id"].(string)
	require.True(t, ok, "oluşturulan ürün kimlik taşımalı: %s", rec.Body.String())
	return id
}

// assign ürünü bir satış kanalına bağlar (yönetim ucu üzerinden).
func (f channelFixture) assign(t *testing.T, productID, channelID string) {
	t.Helper()

	rec := f.sys.request(t, http.MethodPost, "/admin/v1/products/"+productID+"/sales-channels",
		`{"sales_channel_id": "`+channelID+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
}

// list koleksiyonun vitrin listesini verilen kanallarla okur.
func (f channelFixture) list(t *testing.T, channels []string) (handles []string, count int) {
	t.Helper()
	return storeListing(t, f.sys.storeChannelRequest(t,
		"/store/v1/products?collection_id="+f.collectionID, channels))
}

// TestStoreListingShowsUnassignedProductInEveryChannel kuralın geriye uyumlu
// yarısını gerçek veritabanında doğrular: ataması olmayan ürün her kanalda
// görünür.
func TestStoreListingShowsUnassignedProductInEveryChannel(t *testing.T) {
	fx := newChannelFixture(t)
	handle := uniqueHandle("atamasiz")
	fx.seedPublished(t, handle)

	for _, channels := range [][]string{{fx.channelA}, {fx.channelB}, {fx.channelA, fx.channelB}} {
		handles, count := fx.list(t, channels)
		assert.Equal(t, []string{handle}, handles, "atamasız ürün %v kanallarında görünmeli", channels)
		assert.Equal(t, 1, count, "sayaç da atamasız ürünü saymalı")
	}
}

// TestStoreListingHidesProductFromForeignChannel süzgecin ASIL işini gerçek
// SQL'de doğrular: A kanalına atanan ürün A'da görünür, B'de görünmez.
//
// Arıza tam olarak buydu: publishable anahtarın kanalları çözülüyor ama hiçbir
// modül okumuyordu, dolayısıyla her anahtar aynı kataloğu alıyordu.
func TestStoreListingHidesProductFromForeignChannel(t *testing.T) {
	fx := newChannelFixture(t)
	handle := uniqueHandle("kanal-a")
	productID := fx.seedPublished(t, handle)
	fx.assign(t, productID, fx.channelA)

	handles, count := fx.list(t, []string{fx.channelA})
	assert.Equal(t, []string{handle}, handles, "ürün atandığı kanalda görünmeli")
	assert.Equal(t, 1, count)

	handles, count = fx.list(t, []string{fx.channelB})
	assert.Empty(t, handles, "ürün atanmadığı kanalda GÖRÜNMEMELİ")
	assert.Zero(t, count, "sayaç da gizlenen ürünü saymamalı")
}

// TestStoreListingShowsProductInAllAssignedChannels ManyToMany bağın gerçekten
// çoklu olduğunu GERÇEK link tablosunda doğrular.
//
// Kardinalite yanlış bildirilseydi ikinci atama benzersizlik indeksine çarpar
// ve 409 dönerdi; sahte bir link servisi bunu kanıtlayamaz.
func TestStoreListingShowsProductInAllAssignedChannels(t *testing.T) {
	fx := newChannelFixture(t)
	handle := uniqueHandle("iki-kanal")
	productID := fx.seedPublished(t, handle)
	fx.assign(t, productID, fx.channelA)
	fx.assign(t, productID, fx.channelB)

	for _, channel := range []string{fx.channelA, fx.channelB} {
		handles, count := fx.list(t, []string{channel})
		assert.Equal(t, []string{handle}, handles, "%q kanalında görünmeli", channel)
		assert.Equal(t, 1, count)
	}

	// Bağlar gerçekten link tablosundadır; yönetim ucu onları geri okur.
	rec := fx.sys.request(t, http.MethodGet, "/admin/v1/products/"+productID+"/sales-channels", "")
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	assert.ElementsMatch(t, []any{fx.channelA, fx.channelB},
		itemData(t, rec)["sales_channel_ids"])

	linked, err := fx.sys.links.List(context.Background(), service.LinkProductSalesChannel, productID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{fx.channelA, fx.channelB}, linked,
		"bağlar çekirdeğin link servisinden de okunabilmeli")
}

// TestStoreListingFilterKeepsPagingConsistent süzgecin SAYFALAMAYI bozmadığını
// gerçek LIMIT/OFFSET ile doğrular.
//
// Süzme Go tarafında yapılsaydı LIMIT süzülmemiş küme üzerinde uygulanır,
// sayfalar eksik dolar ve sayaç istemcinin hiç ulaşamayacağı sayfalar vaat
// ederdi. Test iki iddiayı birlikte kilitler: sayaç süzülmüş kümeyi yansıtır ve
// sayfalar tam olarak o kadar kayıt taşır.
func TestStoreListingFilterKeepsPagingConsistent(t *testing.T) {
	fx := newChannelFixture(t)

	var hidden, expected []string
	for i := range 6 {
		handle := uniqueHandle(fmt.Sprintf("sayfa-%d", i))
		productID := fx.seedPublished(t, handle)
		if i%3 == 0 {
			fx.assign(t, productID, fx.channelB)
			hidden = append(hidden, handle)
			continue
		}
		expected = append(expected, handle)
	}
	require.Len(t, hidden, 2)
	require.Len(t, expected, 4)

	const pageSize = 3
	var collected []string
	for offset := 0; offset < 6; offset += pageSize {
		rec := fx.sys.storeChannelRequest(t, fmt.Sprintf(
			"/store/v1/products?collection_id=%s&limit=%d&offset=%d", fx.collectionID, pageSize, offset),
			[]string{fx.channelA})
		handles, count := storeListing(t, rec)

		assert.Equal(t, 4, count, "toplam sayaç SÜZÜLMÜŞ kümeyi yansıtmalı (offset=%d)", offset)
		for _, handle := range handles {
			assert.NotContains(t, hidden, handle, "yabancı kanalın ürünü hiçbir sayfada görünmemeli")
		}
		collected = append(collected, handles...)
	}

	assert.ElementsMatch(t, expected, collected,
		"sayfalar sayacın vaat ettiği kayıtların tamamını ve yalnızca onları taşımalı")
	// İlk sayfa TAM dolmalıdır: eleme veritabanında yapılmasaydı sayfa
	// süzülenler kadar eksik gelirdi.
	rec := fx.sys.storeChannelRequest(t, fmt.Sprintf(
		"/store/v1/products?collection_id=%s&limit=%d&offset=0", fx.collectionID, pageSize),
		[]string{fx.channelA})
	firstPage, _ := storeListing(t, rec)
	assert.Len(t, firstPage, pageSize, "ilk sayfa istenen sayfa boyu kadar kayıt taşımalı")
}

// TestStoreSingleProductIsFilteredToo TEKİL ucun da süzüldüğünü doğrular.
//
// Listede gizleyip tekil uçta göstermek gizlemeyi anlamsız kılardı: vitrin
// adresleri handle taşır, yani tahmin edilebilir olan tam da bu uçtur. Yabancı
// kanalda ürün, yayında olmayan ürünle AYNI hatayı (404) döner.
func TestStoreSingleProductIsFilteredToo(t *testing.T) {
	fx := newChannelFixture(t)
	handle := uniqueHandle("tekil-kanal")
	productID := fx.seedPublished(t, handle)
	fx.assign(t, productID, fx.channelA)

	rec := fx.sys.storeChannelRequest(t, "/store/v1/products/"+handle, []string{fx.channelA})
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, productID, itemData(t, rec)["id"])

	rec = fx.sys.storeChannelRequest(t, "/store/v1/products/"+handle, []string{fx.channelB})
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"yabancı kanalda ürün bulunamamalı; gövde: %s", rec.Body.String())

	rec = fx.sys.storeChannelRequest(t, "/store/v1/products/"+productID, []string{fx.channelB})
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"kimlikle çağrı da süzülmeli; gövde: %s", rec.Body.String())
}

// TestStoreListingWithEmptyChannelSetShowsOnlyUnassigned kanalsız bir kimliğin
// savunmacı davranışını gerçek SQL'de sabitler: boş küme "süzme yok" değildir.
func TestStoreListingWithEmptyChannelSetShowsOnlyUnassigned(t *testing.T) {
	fx := newChannelFixture(t)
	assignedHandle := uniqueHandle("bos-atanmis")
	freeHandle := uniqueHandle("bos-atanmamis")
	assignedID := fx.seedPublished(t, assignedHandle)
	fx.seedPublished(t, freeHandle)
	fx.assign(t, assignedID, fx.channelA)

	handles, count := fx.list(t, []string{})
	assert.Equal(t, []string{freeHandle}, handles,
		"kanalsız kimlik yalnızca atamasız ürünleri görmeli")
	assert.Equal(t, 1, count)
}

// TestStoreListingWithoutPrincipalIsNotFiltered kimliksiz bir isteğin
// süzülmediğini doğrular.
//
// Bu, mağaza kimlik doğrulamasının hiç bağlanmadığı kurulumdur (product tek
// başına dağıtılabilir). Aynı zamanda SQL'deki "parametre NULL" dalının
// gerçekten çalıştığının kanıtıdır: nil dilim veritabanına NULL olarak
// gitmeseydi bu istek atanmış ürünü kaçırırdı.
func TestStoreListingWithoutPrincipalIsNotFiltered(t *testing.T) {
	fx := newChannelFixture(t)
	handle := uniqueHandle("kimliksiz")
	productID := fx.seedPublished(t, handle)
	fx.assign(t, productID, fx.channelA)

	handles, count := storeListing(t, fx.sys.storeRequestWithoutPrincipal(t,
		"/store/v1/products?collection_id="+fx.collectionID))
	assert.Equal(t, []string{handle}, handles, "kimlik yoksa süzgeç uygulanmamalı")
	assert.Equal(t, 1, count)
}

// TestAdminListingIsNotFilteredBySalesChannel yönetim listelemesinin
// süzülmediğini doğrular.
//
// Süzülseydi bir ürünü bir kanala atamak onu yönetim listesinden de düşürür ve
// operatör ürünü bir daha bulamazdı.
func TestAdminListingIsNotFilteredBySalesChannel(t *testing.T) {
	fx := newChannelFixture(t)
	handle := uniqueHandle("yonetim-kanal")
	productID := fx.seedPublished(t, handle)
	fx.assign(t, productID, fx.channelA)

	rec := fx.sys.request(t, http.MethodGet,
		"/admin/v1/products?collection_id="+fx.collectionID, "")
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())

	body := jsonBody(t, rec)
	assert.Equal(t, float64(1), body["count"])
	data, ok := body["data"].([]any)
	require.True(t, ok, "data dizi olmalı: %#v", body["data"])
	require.Len(t, data, 1)

	item, ok := data[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, handle, item["handle"])
}

// TestRemoveSalesChannelMakesProductGloballyVisible son bağı kaldırmanın ürünü
// gizlemediğini, tersine her kanalda görünür kıldığını doğrular.
func TestRemoveSalesChannelMakesProductGloballyVisible(t *testing.T) {
	fx := newChannelFixture(t)
	handle := uniqueHandle("bag-kaldir")
	productID := fx.seedPublished(t, handle)
	fx.assign(t, productID, fx.channelA)

	handles, _ := fx.list(t, []string{fx.channelB})
	require.Empty(t, handles, "ürün önce yabancı kanalda gizli olmalı")

	rec := fx.sys.request(t, http.MethodDelete,
		"/admin/v1/products/"+productID+"/sales-channels/"+fx.channelA, "")
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())

	handles, count := fx.list(t, []string{fx.channelB})
	assert.Equal(t, []string{handle}, handles, "ataması kalmayan ürün yine tüm kanallarda görünür")
	assert.Equal(t, 1, count)

	linked, err := fx.sys.links.List(context.Background(), service.LinkProductSalesChannel, productID)
	require.NoError(t, err)
	assert.Empty(t, linked, "bağ gerçekten link tablosundan silinmeli")
}

// TestProductDeletionRemovesSalesChannelLinks silinen ürünün kanal bağlarının
// GERÇEK link tablosundan temizlendiğini doğrular.
func TestProductDeletionRemovesSalesChannelLinks(t *testing.T) {
	fx := newChannelFixture(t)
	productID := fx.seedPublished(t, uniqueHandle("silinen-kanal"))
	fx.assign(t, productID, fx.channelA)

	rec := fx.sys.request(t, http.MethodDelete, "/admin/v1/products/"+productID, "")
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())

	linked, err := fx.sys.links.List(context.Background(), service.LinkProductSalesChannel, productID)
	require.NoError(t, err)
	assert.Empty(t, linked, "silinen ürünün kanal bağı kalmamalı")
}

// TestSalesChannelLinkRejectsUnknownProduct var olmayan bir ürüne bağ
// kurulmasının 404 döndüğünü doğrular.
func TestSalesChannelLinkRejectsUnknownProduct(t *testing.T) {
	sys := newSystem(t)

	rec := sys.request(t, http.MethodPost, "/admin/v1/products/prod_yok/sales-channels",
		`{"sales_channel_id": "sc_1"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code, "gövde: %s", rec.Body.String())
}

// --- YAZMA yolunun kapsam sorusu -------------------------------------------

// seedPublishedWithVariant koleksiyona yayında bir ürün ve ona bağlı bir
// varyant ekler; ürün ile varyantın kimliklerini döner.
//
// Varyant gerekiyor çünkü sepete giren şey ÜRÜN değil VARYANTTIR ve kapsam
// sorusu yazma yolunda varyant kimliğiyle sorulur. Kanal ataması ise ürüne
// yapılır; bu testlerin sınadığı şey tam olarak o devralmanın gerçek SQL'de
// çalışmasıdır.
func (f channelFixture) seedPublishedWithVariant(t *testing.T, handle string) (productID, variantID string) {
	t.Helper()

	productID = f.seedPublished(t, handle)

	rec := f.sys.request(t, http.MethodPost, "/admin/v1/products/"+productID+"/variants",
		`{"title": "Tek beden"}`)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	variantID, ok := itemData(t, rec)["id"].(string)
	require.True(t, ok, "oluşturulan varyant kimlik taşımalı: %s", rec.Body.String())
	return productID, variantID
}

// variantIDsInChannels varyant sağlayıcısını Query'nin kaydettiği ADLA çözer ve
// verilen kanallarda görünen kimlikleri döner.
//
// Sağlayıcı container'dan adla çözülür ki test, sepet akışının gerçekte
// izlediği yolu izlesin: akış somut tipi değil "variant.query" adını bilir
// (ADR 0004/0006). Yapıcıyı doğrudan çağıran bir test, kaydın adı yanlış olsa
// bile yeşil kalırdı.
func (f channelFixture) variantIDsInChannels(t *testing.T, ids, channels []string) []string {
	t.Helper()

	provider, err := container.Resolve[query.Provider](f.sys.container,
		service.EntityVariant+query.ProviderSuffix)
	require.NoError(t, err, "varyant sağlayıcısı kayıtlı olmalı")

	filters := map[string]any{"ids": ids}
	if channels != nil {
		filters[service.FilterSalesChannelIDs] = channels
	}

	records, err := provider.List(context.Background(), query.ListOptions{
		Fields:  []string{query.IDField},
		Filters: filters,
	})
	require.NoError(t, err)

	out := make([]string, 0, len(records))
	for i := range records {
		id, ok := records[i][query.IDField].(string)
		require.True(t, ok, "kayıt kimlik taşımalı: %v", records[i])
		out = append(out, id)
	}
	return out
}

// TestVariantVisibilityFollowsProductChannels varyant kapsamının GERÇEK SQL'de
// ürünün kanallarından türediğini doğrular.
//
// Bu, sepete satır ekleyen yazma yolunun sorduğu sorunun ta kendisidir ve
// sahte bir depoyla kanıtlanamaz: koşul, varyantın product_id'si üzerinden
// link tablosuna bakan bir EXISTS/NOT EXISTS'tir ve tabloyu core/link çalışma
// anında kurar.
func TestVariantVisibilityFollowsProductChannels(t *testing.T) {
	fx := newChannelFixture(t)

	atanmisID, atanmisVaryant := fx.seedPublishedWithVariant(t, uniqueHandle("kanalli-varyant"))
	fx.assign(t, atanmisID, fx.channelA)
	_, atamasizVaryant := fx.seedPublishedWithVariant(t, uniqueHandle("atamasiz-varyant"))

	hepsi := []string{atanmisVaryant, atamasizVaryant}

	assert.ElementsMatch(t, hepsi, fx.variantIDsInChannels(t, hepsi, []string{fx.channelA}),
		"varyant, ürününün atandığı kanalda görünmeli")
	assert.Equal(t, []string{atamasizVaryant}, fx.variantIDsInChannels(t, hepsi, []string{fx.channelB}),
		"YABANCI kanalda yalnızca atamasız ürünün varyantı kalmalı; kalmıyorsa "+
			"yazma yolu kapsamsızdır ve başka bir vitrinin ürünü sepete girebilir")
	assert.Equal(t, []string{atamasizVaryant}, fx.variantIDsInChannels(t, hepsi, []string{}),
		"kanalsız kimlik BOŞ KÜMEDİR: yalnızca atamasız ürünün varyantı görünür")
	assert.ElementsMatch(t, hepsi, fx.variantIDsInChannels(t, hepsi, nil),
		"süzgeç hiç istenmediğinde kapsam uygulanmamalı")
}

// TestVariantVisibilityMatchesStoreListing yazma yolunun cevabının vitrin
// listesiyle AYNI olduğunu doğrular.
//
// İki yüzeyin aynı kurulumda ayrışması, bu değişikliğin kapatmaya çalıştığı
// hata sınıfının kendisidir: kural bir yerde uygulanıp diğerinde
// uygulanmadığında, vitrinde gizlenen ürün sepette satılabilir kalır. Test
// ikisini yan yana koyar ve ayrışmayı gözle görülür kılar.
func TestVariantVisibilityMatchesStoreListing(t *testing.T) {
	fx := newChannelFixture(t)

	gizliID, gizliVaryant := fx.seedPublishedWithVariant(t, uniqueHandle("gizli"))
	fx.assign(t, gizliID, fx.channelA)

	handles, _ := fx.list(t, []string{fx.channelB})
	require.Empty(t, handles, "ürün yabancı vitrinde GÖRÜNMEMELİ (okuma yüzeyi)")

	assert.Empty(t, fx.variantIDsInChannels(t, []string{gizliVaryant}, []string{fx.channelB}),
		"vitrinde gizlenen ürünün varyantı yazma yolunda da GÖRÜNMEMELİ; "+
			"görünüyorsa gizleme yalnızca kozmetiktir")
}
