package searchpg

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// Bu dosya eklentinin akışlarını PostgreSQL OLMADAN sınar: indeks deposu ve
// katalog yüzeyi sahteyle değiştirilir. Testler paket İÇİNDEDİR çünkü iki
// yüzey de (depo, StoreProductReader'ın somut kullanımı) dışa açık değildir;
// dışa açmak, yalnızca test edilebilirlik için sözleşme genişletmek olurdu.
//
// Gerçek SQL'e bağlı iddialar (tsvector eşleşmesi, alaka sıralaması, süpürme)
// searchpg_integration_test.go dosyasındadır.

// sahteDepo indeks tablosunun bellek içi taklididir.
type sahteDepo struct {
	mu sync.Mutex

	belgeler map[string]belge
	// aramaSonucu Search'ün döneceği kimliklerdir; sıra ARAMA tarafından
	// verilir ve handler'ın onu koruduğu bu sayede sınanabilir.
	aramaSonucu []string
	sonSorgu    string
	sonLimit    int
	sonOffset   int
	aramaCagri  int

	upsertHatasi error
	aramaHatasi  error
	silmeHatasi  error

	supurmeCagri int
	supurmeEsigi time.Time
	simdi        time.Time
}

// newSahteDepo boş bir sahte depo üretir.
func newSahteDepo() *sahteDepo {
	return &sahteDepo{
		belgeler: map[string]belge{},
		simdi:    time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC),
	}
}

// Upsert belgeleri belleğe yazar.
func (d *sahteDepo) Upsert(_ context.Context, belgeler []belge) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.upsertHatasi != nil {
		return d.upsertHatasi
	}
	for _, b := range belgeler {
		d.belgeler[b.urunID] = b
	}

	return nil
}

// Delete verilen kimlikleri bellekten siler.
func (d *sahteDepo) Delete(_ context.Context, urunIDs ...string) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.silmeHatasi != nil {
		return 0, d.silmeHatasi
	}
	var silinen int64
	for _, id := range urunIDs {
		if _, ok := d.belgeler[id]; ok {
			delete(d.belgeler, id)
			silinen++
		}
	}

	return silinen, nil
}

// Search önceden verilmiş sonucu döner ve çağrının parametrelerini kaydeder.
func (d *sahteDepo) Search(_ context.Context, sorgu string, limit, offset int) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.aramaCagri++
	d.sonSorgu, d.sonLimit, d.sonOffset = sorgu, limit, offset
	if d.aramaHatasi != nil {
		return nil, d.aramaHatasi
	}

	return slices.Clone(d.aramaSonucu), nil
}

// Sweep süpürme çağrısını kaydeder; bellekte bir şey silmez.
func (d *sahteDepo) Sweep(_ context.Context, esik time.Time) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.supurmeCagri++
	d.supurmeEsigi = esik

	return 0, nil
}

// Now sabit bir zaman döner.
func (d *sahteDepo) Now(_ context.Context) (time.Time, error) {
	return d.simdi, nil
}

// kimlikler indekste bulunan ürün kimliklerini sıralı döner.
func (d *sahteDepo) kimlikler() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	out := make([]string, 0, len(d.belgeler))
	for id := range d.belgeler {
		out = append(out, id)
	}
	slices.Sort(out)

	return out
}

// belgeAl kimliğin belgesini döner.
func (d *sahteDepo) belgeAl(id string) (belge, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	b, ok := d.belgeler[id]

	return b, ok
}

// sahteKatalog "product.interop" yüzeyinin taklididir.
//
// Kanal süzgecini GERÇEĞİYLE AYNI anlamda uygular (nil: süzme yok, boş dilim:
// kanalsız ürünler); testlerin sınadığı davranış eklentinin bu ayrımı doğru
// TAŞIYIP taşımadığıdır, kuralın kendisi değil.
type sahteKatalog struct {
	mu sync.Mutex

	urunler  map[string]json.RawMessage
	kanallar map[string][]string

	sonIstek katalogIstegi
	cagri    int
	hata     error
}

// newSahteKatalog boş bir sahte katalog üretir.
func newSahteKatalog() *sahteKatalog {
	return &sahteKatalog{
		urunler:  map[string]json.RawMessage{},
		kanallar: map[string][]string{},
	}
}

// urunEkle katalogda görünen bir ürün tanımlar.
func (k *sahteKatalog) urunEkle(id, baslik, aciklama string) {
	k.urunKaydiEkle(id, json.RawMessage(`{
		"id": "`+id+`",
		"handle": "`+id+`-handle",
		"title": "`+baslik+`",
		"description": "`+aciklama+`",
		"variants": [{"id": "variant_`+id+`", "title": "Tek", "sku": "SKU-`+id+`"}],
		"tags": [{"id": "ptag_1", "value": "yeni"}],
		"price_set": {"amount": 1000}
	}`))
}

// urunKaydiEkle ham bir katalog kaydı tanımlar.
func (k *sahteKatalog) urunKaydiEkle(id string, kayit json.RawMessage) {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.urunler[id] = kayit
}

// kanalAta ürünü verilen satış kanallarına bağlar.
func (k *sahteKatalog) kanalAta(id string, kanallar ...string) {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.kanallar[id] = kanallar
}

// StoreProductsByIDsJSON istenen kimliklerin kayıtlarını sırayla döner.
func (k *sahteKatalog) StoreProductsByIDsJSON(
	_ context.Context, request json.RawMessage,
) (json.RawMessage, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.cagri++
	if err := json.Unmarshal(request, &k.sonIstek); err != nil {
		return nil, err
	}
	if k.hata != nil {
		return nil, k.hata
	}

	out := make([]json.RawMessage, 0, len(k.sonIstek.IDs))
	for _, id := range k.sonIstek.IDs {
		kayit, ok := k.urunler[id]
		if !ok || !k.gorunur(id, k.sonIstek.SalesChannelIDs) {
			continue
		}
		out = append(out, kayit)
	}

	return json.Marshal(katalogYaniti{Products: out})
}

// gorunur ürünün istenen kanallarda görünüp görünmediğini bildirir.
func (k *sahteKatalog) gorunur(id string, istenen []string) bool {
	if istenen == nil {
		return true
	}
	atanan, ok := k.kanallar[id]
	if !ok || len(atanan) == 0 {
		return true
	}
	for _, kanal := range atanan {
		if slices.Contains(istenen, kanal) {
			return true
		}
	}

	return false
}

// sahteGraph çekirdeğin Query katmanının taklididir.
type sahteGraph struct {
	mu sync.Mutex

	ids        []string
	offsetler  []int
	sonSpec    query.GraphSpec
	hata       error
	hataOffset int
}

// Graph verilen sayfayı kimlik kayıtları olarak döner.
func (g *sahteGraph) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.sonSpec = spec
	g.offsetler = append(g.offsetler, spec.Offset)
	if g.hata != nil && spec.Offset == g.hataOffset {
		return nil, g.hata
	}

	if spec.Offset >= len(g.ids) {
		return []query.Record{}, nil
	}
	son := min(spec.Offset+spec.Limit, len(g.ids))

	out := make([]query.Record, 0, son-spec.Offset)
	for _, id := range g.ids[spec.Offset:son] {
		out = append(out, query.Record{query.IDField: id})
	}

	return out, nil
}

// testModul sahte bağımlılıklarla kurulmuş bir modül üretir.
func testModul(d depo, k StoreProductReader) *modul {
	m := newModul(nil, slog.New(slog.DiscardHandler))
	m.indeks = d
	m.katalog = &katalog{okuyucu: k}

	return m
}

// testRouter modülün uçlarını bağlanmış bir router döner.
func testRouter(m *modul) chi.Router {
	r := chi.NewRouter()
	m.Routes(r)

	return r
}

// istek verilen hedefe istek atar; kimlik verilirse context'e konur.
func istek(m *modul, method, target string, principal *corehttp.Principal) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, http.NoBody)
	if principal != nil {
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), *principal))
	}
	rec := httptest.NewRecorder()
	testRouter(m).ServeHTTP(rec, req)

	return rec
}

// magazaKimligi verilen kanallara bağlı bir mağaza kimliği üretir.
func magazaKimligi(kanallar ...string) *corehttp.Principal {
	if kanallar == nil {
		kanallar = []string{}
	}

	return &corehttp.Principal{ID: "pk_test", Kind: "api_key", SalesChannelIDs: kanallar}
}

// olay verilen ada ve ürün kimliğine sahip bir olay üretir.
func olay(ad, urunID string) eventbus.Event {
	return eventbus.Event{Name: ad, Data: map[string]any{eventFieldProductID: urunID}}
}

// TestUrunYazildiKataloguOkuyupIndeksler abonenin olayı alınca kaydı okuduğunu
// ve ağırlıklı belgeyi yazdığını doğrular.
func TestUrunYazildiKataloguOkuyupIndeksler(t *testing.T) {
	t.Parallel()

	d, k := newSahteDepo(), newSahteKatalog()
	k.urunEkle("prod_1", "Mavi Gömlek", "Pamuklu yazlık gömlek")
	m := testModul(d, k)

	require.NoError(t, m.urunYazildi(t.Context(), olay(eventProductCreated, "prod_1")))

	b, ok := d.belgeAl("prod_1")
	require.True(t, ok, "ürün indekslenmiş olmalı")
	assert.Equal(t, "Mavi Gömlek", b.baslik, "başlık A ağırlığına gider")
	assert.Equal(t, "Pamuklu yazlık gömlek", b.metin, "açıklama C ağırlığına gider")
	assert.Contains(t, b.anahtar, "SKU-prod_1", "SKU aranabilir olmalı")
	assert.Contains(t, b.anahtar, "prod_1-handle", "handle aranabilir olmalı")
	assert.Contains(t, b.anahtar, "yeni", "etiket değeri aranabilir olmalı")
}

// TestIndeksKanaldanBagimsizdir olay işleyicisinin katalogtan kanal SÜZMEDEN
// okuduğunu doğrular.
//
// İndeks kanal başına tutulsaydı aynı ürün kanal sayısı kadar yazılır ve kanal
// ataması değiştiğinde indeksin yeniden kurulması gerekirdi; süzme okuma
// anında yapılır.
func TestIndeksKanaldanBagimsizdir(t *testing.T) {
	t.Parallel()

	d, k := newSahteDepo(), newSahteKatalog()
	k.urunEkle("prod_1", "Mavi Gömlek", "")
	k.kanalAta("prod_1", "sc_web")
	m := testModul(d, k)

	require.NoError(t, m.urunYazildi(t.Context(), olay(eventProductUpdated, "prod_1")))

	assert.Equal(t, []string{"prod_1"}, d.kimlikler())
	assert.Nil(t, k.sonIstek.SalesChannelIDs,
		"indeksleme okuması kanal kimliği taşımamalı (nil = süzgeç yok)")
}

// TestOlaydakiStatusYerineKatalogOkunur bayat bir status alanının indeksi
// YANLIŞ yönde etkilemediğini doğrular.
//
// Olay "taslak" diyor ama katalog ürünü hâlâ vitrinde gösteriyor: doğru davranış
// ürünü indekslemektir. Kısayol alınsaydı ürün, ters sırada teslim edilen iki
// olay yüzünden aramadan sessizce düşerdi.
func TestOlaydakiStatusYerineKatalogOkunur(t *testing.T) {
	t.Parallel()

	d, k := newSahteDepo(), newSahteKatalog()
	k.urunEkle("prod_1", "Mavi Gömlek", "")
	m := testModul(d, k)

	e := olay(eventProductUpdated, "prod_1")
	e.Data["status"] = "draft"
	require.NoError(t, m.urunYazildi(t.Context(), e))

	assert.Equal(t, []string{"prod_1"}, d.kimlikler(),
		"karar olayın söylediğine değil kataloğun O ANKİ durumuna dayanmalı")
}

// TestVitrindeGorunmeyenUrunIndekstenDuser yayından kalkan ürünün indeksten
// silindiğini doğrular.
func TestVitrindeGorunmeyenUrunIndekstenDuser(t *testing.T) {
	t.Parallel()

	d, k := newSahteDepo(), newSahteKatalog()
	require.NoError(t, d.Upsert(t.Context(), []belge{{urunID: "prod_1", baslik: "Eski"}}))
	m := testModul(d, k)

	// Katalog bu kimliği HİÇ döndürmüyor: yayından kalkmış, arşivlenmiş ya da
	// silinmiş demektir.
	require.NoError(t, m.urunYazildi(t.Context(), olay(eventProductUpdated, "prod_1")))

	assert.Empty(t, d.kimlikler(), "vitrinde görünmeyen ürün indekste kalmamalı")
}

// TestUrunSilindiKataloguOkumaz silme olayının katalogla hiç konuşmadığını
// doğrular.
//
// Soft silinmiş kayıt zaten hiçbir okumadan dönmez; okuma turu boşa gidecek bir
// gidiş-dönüş olurdu.
func TestUrunSilindiKataloguOkumaz(t *testing.T) {
	t.Parallel()

	d, k := newSahteDepo(), newSahteKatalog()
	require.NoError(t, d.Upsert(t.Context(), []belge{{urunID: "prod_1", baslik: "Eski"}}))
	m := testModul(d, k)

	require.NoError(t, m.urunSilindi(t.Context(), olay(eventProductDeleted, "prod_1")))

	assert.Empty(t, d.kimlikler())
	assert.Zero(t, k.cagri, "silme olayı katalogu okumamalı")
}

// TestSilmeIdempotenttir aynı silme olayının ikinci teslimi hata üretmediğini
// doğrular.
//
// Redis backend'i EN AZ BİR KEZ teslim eder; ikinci teslimde hata dönen bir
// işleyici, her yeniden başlatmada gürültü üretirdi.
func TestSilmeIdempotenttir(t *testing.T) {
	t.Parallel()

	m := testModul(newSahteDepo(), newSahteKatalog())

	require.NoError(t, m.urunSilindi(t.Context(), olay(eventProductDeleted, "prod_yok")))
	require.NoError(t, m.urunSilindi(t.Context(), olay(eventProductDeleted, "prod_yok")))
}

// TestBozukOlayYukuReddedilir sözleşmeye uymayan yükün hata döndürdüğünü
// doğrular.
func TestBozukOlayYukuReddedilir(t *testing.T) {
	t.Parallel()

	tests := map[string]map[string]any{
		"alan yok":     {},
		"dize değil":   {eventFieldProductID: 42},
		"boş":          {eventFieldProductID: "   "},
		"yanlış tipte": {eventFieldProductID: []string{"prod_1"}},
	}

	for ad, yuk := range tests {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			d := newSahteDepo()
			m := testModul(d, newSahteKatalog())

			err := m.urunYazildi(t.Context(), eventbus.Event{Name: eventProductCreated, Data: yuk})

			require.Error(t, err)
			assert.True(t, coreerrors.IsInvalid(err), "yük hatası KindInvalid olmalı: %v", err)
			assert.Empty(t, d.kimlikler(), "bozuk yükten indekse kayıt yazılmamalı")
		})
	}
}

// TestKatalogHatasiYutulmaz indeksleme hatasının olayı sessizce
// tüketmediğini doğrular.
//
// Hata dönmek veri yolunda YENİDEN DENEMEYE yol açmaz (sözleşme gereği olay her
// hâlükârda ACK'lenir); tek etkisi hatanın olay adı ve kimliğiyle birlikte
// loglanmasıdır. nil dönmek, indeksin geride kaldığını her yerde görünmez
// kılardı.
func TestKatalogHatasiYutulmaz(t *testing.T) {
	t.Parallel()

	d, k := newSahteDepo(), newSahteKatalog()
	k.hata = coreerrors.Unavailable("test_catalog_down", "katalog erişilemez")
	m := testModul(d, k)

	err := m.urunYazildi(t.Context(), olay(eventProductCreated, "prod_1"))

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindUnavailable, coreerrors.KindOf(err),
		"katalogun hata sınıfı korunmalı; erişilemezlik sunucu hatası olarak raporlanmamalı")
}

// TestKayitsizModulHataDoner Register çalışmadan gelen olayın panik değil
// tipli hata ürettiğini doğrular.
func TestKayitsizModulHataDoner(t *testing.T) {
	t.Parallel()

	m := newModul(nil, nil)
	m.katalog = &katalog{okuyucu: newSahteKatalog()}

	err := m.urunYazildi(t.Context(), olay(eventProductCreated, "prod_1"))

	require.Error(t, err)
	assert.Equal(t, codeNotRegistered, coreerrors.CodeOf(err))
	assert.Empty(t, chiDesenleri(testRouter(m)), "indeks yokken hiçbir uç bağlanmamalı")
}

// chiDesenleri router ağacındaki route desenlerini döner.
func chiDesenleri(r chi.Router) []string {
	var out []string
	_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out = append(out, method+" "+route)

		return nil
	})

	return out
}

// TestAramaIndekstenKimlikAlipKatalogtanOkur arama akışının tamamını doğrular:
// indeks kimlik ve SIRA verir, katalog kayıtları döner.
func TestAramaIndekstenKimlikAlipKatalogtanOkur(t *testing.T) {
	t.Parallel()

	d, k := newSahteDepo(), newSahteKatalog()
	k.urunEkle("prod_2", "Gömlek", "")
	k.urunEkle("prod_1", "Gömlek Beyaz", "")
	// Alaka sırasını arama verir; handler onu KORUMALIDIR.
	d.aramaSonucu = []string{"prod_2", "prod_1"}
	m := testModul(d, k)

	rec := istek(m, http.MethodGet, SearchPath+"?q=gomlek&limit=5&offset=10", magazaKimligi())

	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, "gomlek", d.sonSorgu)
	assert.Equal(t, 5, d.sonLimit)
	assert.Equal(t, 10, d.sonOffset)

	var yanit struct {
		Data   []map[string]any `json:"data"`
		Count  int              `json:"count"`
		Offset int              `json:"offset"`
		Limit  int              `json:"limit"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &yanit))
	require.Len(t, yanit.Data, 2)
	assert.Equal(t, "prod_2", yanit.Data[0]["id"], "indeksin verdiği alaka sırası korunmalı")
	assert.Equal(t, "prod_1", yanit.Data[1]["id"])
	assert.Equal(t, 2, yanit.Count)
	assert.Equal(t, 10, yanit.Offset)
	assert.Equal(t, 5, yanit.Limit)
	assert.Contains(t, yanit.Data[0], "price_set",
		"kayıtlar vitrin gösteriminin AYNISI olmalı; eklenti onları yeniden biçimlendirmez")
}

// TestAramaKanallariKimliktenOkur kanal süzgecinin isteğin KİMLİĞİNDEN
// geldiğini ve sorgu dizesinin hiç okunmadığını doğrular.
//
// Sorgu dizesi kabul edilseydi, herhangi bir publishable anahtarla gelen bir
// istemci başka bir kanalın katalogunda arama yapabilirdi.
func TestAramaKanallariKimliktenOkur(t *testing.T) {
	t.Parallel()

	d, k := newSahteDepo(), newSahteKatalog()
	k.urunEkle("prod_web", "Gömlek", "")
	k.kanalAta("prod_web", "sc_web")
	k.urunEkle("prod_pos", "Gömlek", "")
	k.kanalAta("prod_pos", "sc_pos")
	d.aramaSonucu = []string{"prod_web", "prod_pos"}
	m := testModul(d, k)

	rec := istek(m, http.MethodGet,
		SearchPath+"?q=gomlek&sales_channel_ids=sc_pos", magazaKimligi("sc_web"))

	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, []string{"sc_web"}, k.sonIstek.SalesChannelIDs,
		"kanallar yalnızca doğrulanmış kimlikten okunmalı")

	var yanit struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &yanit))
	require.Len(t, yanit.Data, 1, "başka kanalın ürünü sonuçta görünmemeli")
	assert.Equal(t, "prod_web", yanit.Data[0]["id"])
}

// TestKimliksizIstekteKanalSuzgeciUygulanmaz mağaza kimliği hiç bağlanmamış bir
// kurulumda aramanın çalışmaya devam ettiğini doğrular.
func TestKimliksizIstekteKanalSuzgeciUygulanmaz(t *testing.T) {
	t.Parallel()

	d, k := newSahteDepo(), newSahteKatalog()
	k.urunEkle("prod_1", "Gömlek", "")
	k.kanalAta("prod_1", "sc_web")
	d.aramaSonucu = []string{"prod_1"}
	m := testModul(d, k)

	rec := istek(m, http.MethodGet, SearchPath+"?q=gomlek", nil)

	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	assert.Nil(t, k.sonIstek.SalesChannelIDs, "kimlik yoksa süzgeç uygulanmamalı (nil)")
}

// TestKanalsizKimlikBosKumedir kanalı olmayan bir kimliğin "süzme yok" ile
// KARIŞTIRILMADIĞINI doğrular.
//
// İkisi bir tutulsaydı, kanalsız bir anahtara tüm kanalların katalogu açılırdı.
func TestKanalsizKimlikBosKumedir(t *testing.T) {
	t.Parallel()

	d, k := newSahteDepo(), newSahteKatalog()
	k.urunEkle("prod_1", "Gömlek", "")
	d.aramaSonucu = []string{"prod_1"}
	m := testModul(d, k)

	rec := istek(m, http.MethodGet, SearchPath+"?q=gomlek", magazaKimligi())

	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotNil(t, k.sonIstek.SalesChannelIDs, "kanalsız kimlik nil değil BOŞ küme göndermeli")
	assert.Empty(t, k.sonIstek.SalesChannelIDs)
}

// TestBosSonuctaKatalogHicCagrilmaz eşleşme yokken gereksiz bir tur
// atılmadığını doğrular.
func TestBosSonuctaKatalogHicCagrilmaz(t *testing.T) {
	t.Parallel()

	d, k := newSahteDepo(), newSahteKatalog()
	m := testModul(d, k)

	rec := istek(m, http.MethodGet, SearchPath+"?q=hicbirsey", magazaKimligi())

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Zero(t, k.cagri, "boş kimlik listesi için katalog çağrılmamalı")
	assert.JSONEq(t, `{"data":[],"count":0,"offset":0,"limit":20}`, rec.Body.String(),
		"boş sonuç null değil BOŞ dizi olmalı")
}

// TestGecersizAramaParametreleriReddedilir sınır ve biçim hatalarının 422
// döndürdüğünü doğrular.
func TestGecersizAramaParametreleriReddedilir(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"sorgu yok":        SearchPath,
		"sorgu boş":        SearchPath + "?q=",
		"sorgu boşluk":     SearchPath + "?q=%20%20",
		"limit sıfır":      SearchPath + "?q=a&limit=0",
		"limit sınır üstü": SearchPath + "?q=a&limit=" + strconv.Itoa(maxLimit+1),
		"limit sayı değil": SearchPath + "?q=a&limit=abc",
		"offset negatif":   SearchPath + "?q=a&offset=-1",
	}

	for ad, hedef := range tests {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			d := newSahteDepo()
			rec := istek(testModul(d, newSahteKatalog()), http.MethodGet, hedef, magazaKimligi())

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "gövde: %s", rec.Body.String())
			assert.Zero(t, d.aramaCagri, "geçersiz istek indekse hiç gitmemeli")
		})
	}
}

// TestUzunSorguReddedilir sınırsız bir metnin sorgu ayrıştırıcısına
// verilmediğini doğrular.
func TestUzunSorguReddedilir(t *testing.T) {
	t.Parallel()

	uzun := make([]byte, maxSorguBaytlari+1)
	for i := range uzun {
		uzun[i] = 'a'
	}

	d := newSahteDepo()
	rec := istek(testModul(d, newSahteKatalog()), http.MethodGet,
		SearchPath+"?q="+string(uzun), magazaKimligi())

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Zero(t, d.aramaCagri)
}

// TestYenidenIndeksleme katalogun sayfa sayfa okunduğunu ve turun sonunda
// süpürüldüğünü doğrular.
func TestYenidenIndeksleme(t *testing.T) {
	t.Parallel()

	d, k := newSahteDepo(), newSahteKatalog()
	graph := &sahteGraph{}
	// İki tam sayfa + eksik bir sayfa: son sayfanın kısa olması döngüyü
	// bitirmeli, fazladan bir tur atılmamalı.
	toplam := reindexSayfaBoyu*2 + 7
	for i := range toplam {
		id := "prod_" + strconv.Itoa(i)
		graph.ids = append(graph.ids, id)
		k.urunEkle(id, "Ürün "+strconv.Itoa(i), "")
	}

	m := testModul(d, k)
	m.graph = graph

	sonuc, err := m.yenidenIndeksle(t.Context())

	require.NoError(t, err)
	assert.Equal(t, toplam, sonuc.Indexed)
	assert.Equal(t, 3, sonuc.Pages)
	assert.Equal(t, []int{0, reindexSayfaBoyu, reindexSayfaBoyu * 2}, graph.offsetler,
		"sayfalama offset'i sayfa boyu kadar ilerlemeli")
	assert.Len(t, d.kimlikler(), toplam)

	assert.Equal(t, catalogEntity, graph.sonSpec.Entity)
	assert.Equal(t, []string{query.IDField}, graph.sonSpec.Fields,
		"kimlikten başka alan istenmemeli")
	assert.Equal(t, map[string]any{catalogStatusFilter: catalogStatusPublished},
		graph.sonSpec.Filters, "yalnızca yayındaki ürünler indekslenmeli")

	assert.Equal(t, 1, d.supurmeCagri, "tur bitince tam bir süpürme yapılmalı")
	assert.Equal(t, d.simdi, d.supurmeEsigi, "eşik VERİTABANI saatinden alınmalı")
}

// TestYarimKalanTurSupurmez hata alan bir turun geçerli kayıtları silmediğini
// doğrular.
//
// Süpürme yarıda kalan turdan sonra çalışsaydı, okunamamış sayfalardaki tüm
// ürünler indeksten düşerdi — yani onarım aracı, kataloğu aramadan silen bir
// araca dönüşürdü.
func TestYarimKalanTurSupurmez(t *testing.T) {
	t.Parallel()

	d, k := newSahteDepo(), newSahteKatalog()
	graph := &sahteGraph{hata: coreerrors.Unavailable("test_query_down", "query düştü"), hataOffset: reindexSayfaBoyu}
	for i := range reindexSayfaBoyu * 2 {
		id := "prod_" + strconv.Itoa(i)
		graph.ids = append(graph.ids, id)
		k.urunEkle(id, "Ürün", "")
	}

	m := testModul(d, k)
	m.graph = graph

	_, err := m.yenidenIndeksle(t.Context())

	require.Error(t, err)
	assert.Zero(t, d.supurmeCagri, "yarım kalan turdan sonra süpürme yapılmamalı")
	assert.Len(t, d.kimlikler(), reindexSayfaBoyu, "ilk sayfa yine de yazılmış olmalı")
}

// TestYenidenIndekslemeUcuYetkiIster yönetim ucunun korumasız olmadığını
// doğrular.
func TestYenidenIndekslemeUcuYetkiIster(t *testing.T) {
	t.Parallel()

	m := testModul(newSahteDepo(), newSahteKatalog())
	m.graph = &sahteGraph{}

	t.Run("kimliksiz", func(t *testing.T) {
		t.Parallel()

		rec := istek(m, http.MethodPost, ReindexPath, nil)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("yetkisiz", func(t *testing.T) {
		t.Parallel()

		rec := istek(m, http.MethodPost, ReindexPath,
			&corehttp.Principal{ID: "usr_1", Kind: "user", Scopes: []string{"product:read"}})
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("modül yetkisi", func(t *testing.T) {
		t.Parallel()

		rec := istek(m, http.MethodPost, ReindexPath,
			&corehttp.Principal{ID: "usr_1", Kind: "user", Scopes: []string{ScopeWrite}})
		assert.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	})

	t.Run("üst yetki", func(t *testing.T) {
		t.Parallel()

		rec := istek(m, http.MethodPost, ReindexPath,
			&corehttp.Principal{ID: "usr_1", Kind: "user", Scopes: []string{corehttp.ScopeAdmin}})
		assert.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	})
}

// TestKatalogTembelCozulur yüzeyin Setup'ta değil İLK KULLANIMDA çözüldüğünü
// ve başarısız bir çözümün kalıcı olmadığını doğrular.
//
// sync.Once kullanılsaydı, product henüz kayıtlı değilken düşen tek bir çözüm
// süreç ömrü boyunca aramayı ölü bırakırdı.
func TestKatalogTembelCozulur(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	k := newKatalog(c)

	_, err := k.urunler(t.Context(), []string{"prod_1"}, nil)
	require.Error(t, err, "kayıt yokken okuma hata dönmeli")
	assert.Equal(t, codeCatalogMissing, coreerrors.CodeOf(err))

	// Kayıt SONRADAN yapılır; bu, modüllerin eklenti Setup'ından sonra ayağa
	// kalkmasının taklididir.
	sahte := newSahteKatalog()
	sahte.urunEkle("prod_1", "Gömlek", "")
	require.NoError(t, c.Provide(catalogInteropName, sahte))

	kayitlar, err := k.urunler(t.Context(), []string{"prod_1"}, nil)
	require.NoError(t, err, "kayıt yapıldıktan sonra çözüm başarılı olmalı")
	require.Len(t, kayitlar, 1)

	// İkinci çağrı önbelleklenmiş yüzeyi kullanır.
	_, err = k.urunler(t.Context(), []string{"prod_1"}, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, sahte.cagri)
}

// TestKatalogBosKimlikIcinCagrilmaz boş listede container'a bile
// gidilmediğini doğrular.
func TestKatalogBosKimlikIcinCagrilmaz(t *testing.T) {
	t.Parallel()

	k := newKatalog(nil)

	kayitlar, err := k.urunler(t.Context(), nil, nil)

	require.NoError(t, err, "boş kimlik listesi çözüm bile gerektirmemeli")
	assert.Empty(t, kayitlar)
}

// TestBozukKatalogKaydiReddedilir kimliksiz bir kaydın indekse yazılmadığını
// doğrular.
func TestBozukKatalogKaydiReddedilir(t *testing.T) {
	t.Parallel()

	d, k := newSahteDepo(), newSahteKatalog()
	k.urunKaydiEkle("prod_1", json.RawMessage(`{"title": "Kimliksiz"}`))
	m := testModul(d, k)

	err := m.urunYazildi(t.Context(), olay(eventProductCreated, "prod_1"))

	require.Error(t, err)
	assert.Equal(t, codeCatalogResponse, coreerrors.CodeOf(err))
	assert.Empty(t, d.kimlikler(), "birincil anahtarı boş bir satır yazılmamalı")
}
