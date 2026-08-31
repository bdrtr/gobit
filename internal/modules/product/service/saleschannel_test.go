package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Bu dosya vitrinin SATIŞ KANALI süzgecini sınar.
//
// Kural: kanal ataması OLMAYAN ürün tüm kanallarda görünür, ataması OLAN ürün
// yalnızca atandığı kanallarda görünür. Buradaki testler kuralın SERVİS
// tarafındaki karşılığını (opsiyonun taşınması, tekil ucun da süzülmesi,
// sayfalamanın bozulmaması) doğrular; kuralın SQL'de gerçekten uygulandığı
// entegrasyon testlerinde kanıtlanır — sahte depo kendi yazdığı koşulu
// doğrulayamaz.

// channelFixture kanal süzme testlerinin ortak kurulumudur.
type channelFixture struct {
	svc   *service.Service
	links *fakeLinker
}

// newChannelFixture yayında ürünler kurulabilen bir servis üretir.
func newChannelFixture(t *testing.T) channelFixture {
	t.Helper()

	links := newFakeLinker()
	return channelFixture{svc: newService(t, newMemStore(), links, nil), links: links}
}

// storeHandles vitrin listesinden dönen ürünlerin handle'larını verir.
func storeHandles(items []service.StoreProduct) []string {
	out := make([]string, 0, len(items))
	for i := range items {
		out = append(out, items[i].Handle)
	}
	return out
}

// TestSalesChannelLinkTableMatchesLinkName deponun elle yazdığı link tablosu
// adının, servisin bildirdiği link adından TÜREDİĞİNİ doğrular.
//
// İki sabit iki ayrı pakette yaşar ve aralarında derleyici bağı yoktur: depo
// service'i import edemez (service zaten depoyu import eder), bu yüzden tablo
// adı elle yazılmıştır. Ayrışırlarsa süzgeç var olmayan bir tabloyu sorar ve
// vitrin listesi tümüyle düşer — ama bu ancak veritabanına gidildiğinde,
// yani entegrasyon testinde görülürdü. Bu test bağı hızlı takıma taşır.
func TestSalesChannelLinkTableMatchesLinkName(t *testing.T) {
	t.Parallel()

	table, err := link.TableName(service.LinkProductSalesChannel)
	require.NoError(t, err, "link adı çekirdeğin tablo adı doğrulamasından geçmeli")
	assert.Equal(t, table, repository.SalesChannelLinkTable,
		"deponun sorguladığı tablo, bildirilen link adından türeyen tablo olmalı")
}

// TestStoreListingShowsUnassignedProductInEveryChannel kuralın GERİYE UYUMLU
// yarısını doğrular: hiç kanal ataması olmayan ürün her kanalda görünür.
//
// Bu, katı alternatifin (atanmamış = gizli) bilinçli olarak seçilmediğinin
// kanıtıdır; seçilseydi mevcut kataloglar bir gecede boşalırdı.
func TestStoreListingShowsUnassignedProductInEveryChannel(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	seedProduct(t, fx.svc, "tisort", "Tişört")

	for _, channels := range [][]string{{"sc_a"}, {"sc_b"}, {"sc_a", "sc_b"}} {
		result, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{SalesChannelIDs: channels})
		require.NoError(t, err)
		assert.Equal(t, []string{"tisort"}, storeHandles(result.Items),
			"atamasız ürün %v kanallarında da görünmeli", channels)
		assert.Equal(t, 1, result.Count, "sayaç da atamasız ürünü saymalı")
	}
}

// TestStoreListingHidesProductFromForeignChannel kuralın SÜZEN yarısını
// doğrular: A kanalına atanan ürün A'da görünür, B'de görünmez.
//
// Arızanın kendisi buydu — publishable anahtarın kanalları çözülüyor ama hiçbir
// modül okumuyordu, dolayısıyla her anahtar AYNI kataloğu alıyordu.
func TestStoreListingHidesProductFromForeignChannel(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	product := seedProduct(t, fx.svc, "tisort", "Tişört")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))

	visible, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{SalesChannelIDs: []string{"sc_a"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"tisort"}, storeHandles(visible.Items), "ürün atandığı kanalda görünmeli")
	assert.Equal(t, 1, visible.Count)

	hidden, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{SalesChannelIDs: []string{"sc_b"}})
	require.NoError(t, err)
	assert.Empty(t, hidden.Items, "ürün atanmadığı kanalda GÖRÜNMEMELİ")
	assert.Zero(t, hidden.Count, "sayaç da gizlenen ürünü saymamalı")
}

// TestStoreListingShowsProductInAllAssignedChannels çoktan çoğa bağın gerçekten
// çoklu olduğunu doğrular.
//
// Kardinalite yanlış bildirilseydi (OneToOne/OneToMany) ikinci atama çakışmayla
// düşer ve ürün ikinci vitrinde hiç görünmezdi.
func TestStoreListingShowsProductInAllAssignedChannels(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	product := seedProduct(t, fx.svc, "tisort", "Tişört")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_b"))

	for _, channel := range []string{"sc_a", "sc_b"} {
		result, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{SalesChannelIDs: []string{channel}})
		require.NoError(t, err)
		assert.Equal(t, []string{"tisort"}, storeHandles(result.Items),
			"iki kanala atanmış ürün %q kanalında da görünmeli", channel)
	}

	ids, err := fx.svc.ProductSalesChannelIDs(ctx, product.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"sc_a", "sc_b"}, ids, "her iki bağ da kalıcı olmalı")
}

// TestRemoveSalesChannelMakesProductGloballyVisible son bağı kaldırmanın ürünü
// GİZLEMEDİĞİNİ, tersine her kanalda görünür kıldığını doğrular.
//
// Kuralın en kolay yanlış anlaşılan sonucu budur; davranış godoc'ta yazılıdır
// ve burada kilitlenir.
func TestRemoveSalesChannelMakesProductGloballyVisible(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	product := seedProduct(t, fx.svc, "tisort", "Tişört")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))

	hidden, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{SalesChannelIDs: []string{"sc_b"}})
	require.NoError(t, err)
	require.Empty(t, hidden.Items)

	require.NoError(t, fx.svc.RemoveProductSalesChannel(ctx, product.ID, "sc_a"))

	result, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{SalesChannelIDs: []string{"sc_b"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"tisort"}, storeHandles(result.Items),
		"ataması kalmayan ürün yine tüm kanallarda görünür")
}

// TestStoreListingFilterKeepsPagingConsistent süzgecin SAYFALAMAYI bozmadığını
// doğrular.
//
// Süzme Go tarafında yapılsaydı LIMIT/OFFSET süzülmemiş küme üzerinde
// uygulanır, sayfalar eksik dolar ve toplam sayaç istemcinin hiç ulaşamayacağı
// sayfaları vaat ederdi. Test iki iddiayı birlikte kilitler: sayaç SÜZÜLMÜŞ
// kümeyi yansıtır ve sayfalar o sayacın söylediği kadar kayıt taşır.
func TestStoreListingFilterKeepsPagingConsistent(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()

	// Altı ürünün ikisi yabancı bir kanala atanır; kalan dördü atamasızdır.
	var hidden []string
	for i := range 6 {
		handle := fmt.Sprintf("urun-%d", i)
		product := seedProduct(t, fx.svc, handle, "Ürün "+handle)
		if i%3 == 0 {
			require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_b"))
			hidden = append(hidden, handle)
		}
	}
	require.Len(t, hidden, 2)

	const pageSize = 3
	seen := map[string]bool{}
	for offset := 0; offset < 6; offset += pageSize {
		page, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{
			SalesChannelIDs: []string{"sc_a"},
			Limit:           pageSize,
			Offset:          offset,
		})
		require.NoError(t, err)
		assert.Equal(t, 4, page.Count,
			"toplam sayaç SÜZÜLMÜŞ kümeyi yansıtmalı (offset=%d)", offset)
		for _, handle := range storeHandles(page.Items) {
			assert.NotContains(t, hidden, handle, "yabancı kanalın ürünü hiçbir sayfada görünmemeli")
			seen[handle] = true
		}
	}
	assert.Len(t, seen, 4, "sayacın vaat ettiği kadar kayıt sayfalardan toplanmalı")
}

// TestGetStoreProductIsFilteredToo TEKİL ucun da süzüldüğünü doğrular.
//
// Listede gizleyip tekil uçta göstermek gizlemeyi anlamsız kılardı: vitrin
// adresleri handle taşır, yani tahmin edilebilir olan tam da bu uçtur.
// Yabancı kanalda görünmeyen ürün, yayında olmayan ürünle AYNI hatayı
// (NotFound) döner; farklı bir sınıf ürünün varlığını ele verirdi.
func TestGetStoreProductIsFilteredToo(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	product := seedProduct(t, fx.svc, "tisort", "Tişört")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))

	found, err := fx.svc.GetStoreProduct(ctx, "tisort", []string{"sc_a"})
	require.NoError(t, err)
	assert.Equal(t, product.ID, found.ID)

	_, err = fx.svc.GetStoreProduct(ctx, "tisort", []string{"sc_b"})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "yabancı kanalda ürün bulunamamalı: %v", err)

	// Kimlikle çağrı da aynı süzgece tabidir; handle'ı kapatıp kimliği açık
	// bırakmak gizlemeyi delerdi.
	_, err = fx.svc.GetStoreProduct(ctx, product.ID, []string{"sc_b"})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "kimlikle çağrı da süzülmeli: %v", err)
}

// TestStoreListingWithoutChannelIdentityIsNotFiltered nil kanal listesinin
// "süzme yok" demek olduğunu doğrular.
//
// Bu, mağaza kimlik doğrulamasının hiç bağlanmadığı kurulumdur (product tek
// başına dağıtılabilir). Süzgeç uygulansaydı böyle bir kurulumda vitrin
// sessizce boşalırdı.
func TestStoreListingWithoutChannelIdentityIsNotFiltered(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	product := seedProduct(t, fx.svc, "tisort", "Tişört")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))

	result, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"tisort"}, storeHandles(result.Items),
		"kanal kimliği taşımayan istekte süzgeç uygulanmamalı")

	single, err := fx.svc.GetStoreProduct(ctx, "tisort", nil)
	require.NoError(t, err)
	assert.Equal(t, product.ID, single.ID)
}

// TestStoreListingWithEmptyChannelSetShowsOnlyUnassigned KANALSIZ bir kimliğin
// savunmacı davranışını sabitler.
//
// Pratikte bu durum oluşmaz: auth, etkin kanalı kalmamış bir publishable
// anahtarı zaten reddeder. Yine de bir gün oluşursa boş küme "süzme yok" DEĞİL,
// "eşleşecek kanal yok" sayılır — tersi, kanalsız bir kimliğe tüm kanalların
// katalogunu açardı. Atamasız ürünler görünmeye devam eder; kuralın kendisi
// değişmez, yalnızca eşleşecek kanal yoktur.
func TestStoreListingWithEmptyChannelSetShowsOnlyUnassigned(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	assigned := seedProduct(t, fx.svc, "atanmis", "Atanmış")
	seedProduct(t, fx.svc, "atanmamis", "Atanmamış")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, assigned.ID, "sc_a"))

	result, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{SalesChannelIDs: []string{}})
	require.NoError(t, err)
	assert.Equal(t, []string{"atanmamis"}, storeHandles(result.Items),
		"kanalsız kimlik yalnızca atamasız ürünleri görmeli")
	assert.Equal(t, 1, result.Count)
}

// TestAdminListingIgnoresSalesChannels yönetim listelemesinin süzülmediğini
// doğrular.
//
// Yönetim kimliğinin satış kanalı yoktur ve kataloğu bütün olarak görmesi
// gerekir; süzülseydi bir ürünü bir kanala atamak onu yönetim listesinden de
// düşürürdü.
func TestAdminListingIgnoresSalesChannels(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	product := seedProduct(t, fx.svc, "tisort", "Tişört")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))

	result, err := fx.svc.ListProducts(ctx, service.ListProductsOptions{})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "tisort", result.Items[0].Handle)
}

// TestAddSalesChannelRejectsUnknownProduct var olmayan bir ürüne bağ
// kurulamadığını doğrular.
//
// Link servisi kimlikleri serbest dizge olarak görür; denetim olmasaydı yazım
// hatası taşıyan bir kimlik sessizce bağlanır ve o bağ hiçbir sorguda
// görünmezdi.
func TestAddSalesChannelRejectsUnknownProduct(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)

	err := fx.svc.AddProductSalesChannel(context.Background(), "prod_yok", "sc_a")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "beklenen sınıf not_found: %v", err)
	assert.Empty(t, fx.links.linked(service.LinkProductSalesChannel, "prod_yok"),
		"reddedilen istek bağ bırakmamalı")
}

// TestAddSalesChannelIsIdempotent aynı bağın ikinci kez kurulmasının hata
// vermediğini doğrular.
//
// Yeniden denenen bir yönetim isteği (ya da bir saga adımı) aynı çifti tekrar
// bağlar; çakışma dönmek onu arıza gibi gösterirdi.
func TestAddSalesChannelIsIdempotent(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	product := seedProduct(t, fx.svc, "tisort", "Tişört")

	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))

	assert.Equal(t, []string{"sc_a"}, fx.links.linked(service.LinkProductSalesChannel, product.ID),
		"ikinci çağrı ikinci bir satır eklememeli")
}

// TestDeleteProductCleansSalesChannelLinks silinen ürünün kanal bağlarının
// temizlendiğini doğrular.
//
// Bağ kalsaydı, auth tarafında ters yönde yapılan bir okuma ("bu kanalda hangi
// ürünler var") silinmiş bir ürüne çıkardı.
func TestDeleteProductCleansSalesChannelLinks(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	product := seedProduct(t, fx.svc, "tisort", "Tişört")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))

	require.NoError(t, fx.svc.DeleteProduct(ctx, product.ID))

	assert.Empty(t, fx.links.linked(service.LinkProductSalesChannel, product.ID),
		"silinen ürünün kanal bağı kalmamalı")
}

// TestSalesChannelLinksRequireLinkService link servisi olmadan kurulmuş bir
// servisin tipli "hazır değil" hatası döndüğünü doğrular.
func TestSalesChannelLinksRequireLinkService(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), nil, nil)
	product := seedProduct(t, svc, "tisort", "Tişört")
	ctx := context.Background()

	err := svc.AddProductSalesChannel(ctx, product.ID, "sc_a")
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "beklenen sınıf unavailable: %v", err)

	_, err = svc.ProductSalesChannelIDs(ctx, product.ID)
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "beklenen sınıf unavailable: %v", err)
}
