package graph_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// sahteVitrin resolver'ların çağırdığı vitrin servisinin sahtesidir.
//
// Çağrı ölçütlerini KAYDEDER: bu dosyadaki iddiaların çoğu dönen veriyle değil,
// servise NE GEÇİLDİĞİYLE ilgilidir (satış kanalı süzgeci gibi).
//
// Kilit gerçek bir ihtiyaçtır: gqlgen kök sorgu alanlarını EŞZAMANLI çözer,
// yani tek istekte iki takma adlı sorgu iki gorutin demektir.
type sahteVitrin struct {
	mu sync.Mutex

	listeOlculeri []service.StoreListOptions
	tekilSecici   []string
	tekilKanallar [][]string

	liste service.ListResult[service.StoreProduct]
	tekil service.StoreProduct
	hata  error
}

// ListStoreProducts çağrının ölçütlerini kaydeder ve hazır sonucu döner.
//
// Sayaç alanında GERÇEK servisin sözleşmesi taklit edilir: SkipCount isteniyorsa
// nil, istenmiyorsa dolu döner. Sabit bir sonuç döndürmek yetmezdi — şemadaki
// "count: Int!" nil'i kabul etmez ve sayacı SEÇEN her test, sahte yüzünden
// "null which the schema does not allow" hatasıyla düşerdi. Yani sahtenin
// sözleşmeyi taşımaması, sınanan davranışla ilgisiz bir arıza üretirdi.
func (s *sahteVitrin) ListStoreProducts(
	_ context.Context,
	opts service.StoreListOptions,
) (service.ListResult[service.StoreProduct], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.listeOlculeri = append(s.listeOlculeri, opts)

	if s.hata != nil {
		return service.ListResult[service.StoreProduct]{}, s.hata
	}

	liste := s.liste

	switch {
	case opts.SkipCount:
		liste.Count = nil
	case liste.Count == nil:
		liste.Count = ptr(len(liste.Items))
	}

	return liste, nil
}

// GetStoreProduct çağrının seçicisini ve kanallarını kaydeder.
func (s *sahteVitrin) GetStoreProduct(
	_ context.Context,
	idOrHandle string,
	salesChannelIDs []string,
) (service.StoreProduct, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tekilSecici = append(s.tekilSecici, idOrHandle)
	s.tekilKanallar = append(s.tekilKanallar, salesChannelIDs)

	if s.hata != nil {
		return service.StoreProduct{}, s.hata
	}

	return s.tekil, nil
}

// sonListe kaydedilen son listeleme ölçütünü döner.
func (s *sahteVitrin) sonListe(t *testing.T) service.StoreListOptions {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	require.Len(t, s.listeOlculeri, 1, "servis TAM OLARAK bir kez çağrılmalıydı")

	return s.listeOlculeri[0]
}

// graphqlYaniti tek bir GraphQL yanıtının çözülmüş hâlidir.
type graphqlYaniti struct {
	Data   map[string]any `json:"data"`
	Errors []struct {
		Message    string         `json:"message"`
		Extensions map[string]any `json:"extensions"`
	} `json:"errors"`
}

// sorgula belgeyi uca POST eder ve yanıtı çözer.
//
// İstek GERÇEK handler'dan geçer (taşıma, ayrıştırma, doğrulama, resolver):
// resolver'ı doğrudan çağırmak, kanalın context'ten okunduğunu değil yalnızca
// fonksiyonun gövdesini sınardı.
func sorgula(
	t *testing.T,
	ctx context.Context,
	svc graph.Storefront,
	belge string,
) (yanit graphqlYaniti, durum int) {
	t.Helper()

	return sorgulaOpts(t, ctx, svc, belge, graph.Options{})
}

// sorgulaOpts belgeyi verilen sınırlarla kurulmuş bir uca POST eder.
func sorgulaOpts(
	t *testing.T,
	ctx context.Context,
	svc graph.Storefront,
	belge string,
	opts graph.Options,
) (yanit graphqlYaniti, durum int) {
	t.Helper()

	rec := istekYap(t, ctx, svc, belge, opts)

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &yanit), "gövde: %s", rec.Body.String())

	return yanit, rec.Code
}

// istekYap belgeyi uca POST eder ve HAM yanıtı döner.
//
// Ham gövde, çözülmüş yanıtın gizlediği bir soruyu sorabilmek içindir: bir
// metnin yanıtın HİÇBİR yerinde — mesajda, uzantılarda, yolda — geçmediğini
// ancak baytlara bakarak iddia edebiliriz (bkz. handler_test.go).
func istekYap(
	t *testing.T,
	ctx context.Context,
	svc graph.Storefront,
	belge string,
	opts graph.Options,
) *httptest.ResponseRecorder {
	t.Helper()

	govde, err := json.Marshal(map[string]any{"query": belge})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, graph.Path, strings.NewReader(string(govde)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	graph.NewHandler(svc, opts).ServeHTTP(rec, req)

	return rec
}

// kimlikli verilen satış kanallarını taşıyan bir mağaza kimliği kurar.
func kimlikli(kanallar []string) context.Context {
	return corehttp.WithPrincipal(context.Background(), corehttp.Principal{
		ID:              "pk_test",
		Kind:            "api_key",
		SalesChannelIDs: kanallar,
	})
}

// TestListeKanallariKimliktenAlir süzgecin isteğin KİMLİĞİNDEN geldiğini
// doğrular.
//
// Kanal sorgudan alınabilseydi süzgeç bir yetkilendirme olmaktan çıkıp
// görüntüleme tercihine dönerdi: elindeki herhangi bir publishable anahtarla
// gelen istemci başka bir vitrinin katalogunu okurdu.
func TestListeKanallariKimliktenAlir(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, kod := sorgula(t, kimlikli([]string{"sc_1", "sc_2"}), svc,
		`{ products { count } }`)

	require.Empty(t, yanit.Errors)
	assert.Equal(t, http.StatusOK, kod)
	assert.Equal(t, []string{"sc_1", "sc_2"}, svc.sonListe(t).SalesChannelIDs)
}

// TestKanalsizKimlikBosKumeGecirir kanalsız bir kimliğin "süzme yok" ile AYNI
// ŞEY OLMADIĞINI doğrular.
//
// nil, "istek hiç kanal kimliği taşımıyor" demektir ve süzgeci kapatır. Kanalsız
// bir kimliği nil'e çevirmek, o kimliğe TÜM kanalların katalogunu açardı; boş
// küme ise kuralı uygular ve yalnızca ataması olmayan ürünler görünür.
func TestKanalsizKimlikBosKumeGecirir(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli(nil), svc, `{ products { count } }`)

	require.Empty(t, yanit.Errors)

	kanallar := svc.sonListe(t).SalesChannelIDs
	assert.NotNil(t, kanallar, "kanalsız kimlik boş küme geçirmeli, nil değil")
	assert.Empty(t, kanallar)
}

// TestKimliksizIstekSuzgeciUygulamaz mağaza kimlik doğrulaması bağlanmamış bir
// kurulumda vitrinin boşalmadığını doğrular.
//
// product tek başına da dağıtılabilir; o kurulumda kanal kimliği hiç yoktur ve
// süzgeç uygulanmamalıdır.
func TestKimliksizIstekSuzgeciUygulamaz(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, context.Background(), svc, `{ products { count } }`)

	require.Empty(t, yanit.Errors)
	assert.Nil(t, svc.sonListe(t).SalesChannelIDs)
}

// TestTekilUcKanallariGecirir tekil sorgunun da AYNI süzgece tabi olduğunu
// doğrular.
//
// Listede gizlenen bir ürünü tekil sorgudan göstermek gizlemeyi tümüyle
// anlamsız kılardı; vitrin adresleri handle taşıdığı için tahmin edilebilir
// olan tam da bu sorgudur.
func TestTekilUcKanallariGecirir(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{tekil: service.StoreProduct{
		Product: models.Product{ID: "prod_1", Handle: "tisort"},
	}}

	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc,
		`{ product(handle: "tisort") { id } }`)

	require.Empty(t, yanit.Errors)
	assert.Equal(t, []string{"tisort"}, svc.tekilSecici)
	assert.Equal(t, [][]string{{"sc_1"}}, svc.tekilKanallar)
}

// TestSorguSatisKanaliIsteyemez şemada olmayan bir argümanın DOĞRULAMADA
// reddedildiğini ve servise hiç ulaşmadığını doğrular.
//
// Şema testi argümanın yokluğunu bildirir; bu test yokluğun ÇALIŞMA ZAMANINDA
// ne anlama geldiğini gösterir: istek reddedilir, katalog okunmaz.
func TestSorguSatisKanaliIsteyemez(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc,
		`{ products(salesChannelIds: ["sc_baskasi"]) { count } }`)

	require.NotEmpty(t, yanit.Errors, "bilinmeyen argüman reddedilmeli")
	assert.Contains(t, yanit.Errors[0].Message, "salesChannelIds")
	assert.Empty(t, svc.listeOlculeri, "reddedilen sorgu servise hiç ulaşmamalı")
}

// TestListeArgumanlariServiseGecer okunan argümanların ölçütlere aynen
// geçtiğini doğrular.
func TestListeArgumanlariServiseGecer(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc,
		`{ products(limit: 5, offset: 10, q: "tişört", collectionId: "pcol_1") { count } }`)

	require.Empty(t, yanit.Errors)

	opts := svc.sonListe(t)
	assert.Equal(t, 5, opts.Limit)
	assert.Equal(t, 10, opts.Offset)
	require.NotNil(t, opts.Search)
	assert.Equal(t, "tişört", *opts.Search)
	require.NotNil(t, opts.CollectionID)
	assert.Equal(t, "pcol_1", *opts.CollectionID)
}

// TestMetinArgumanlariKirpilarakGecer dolu bir argümanın kırpılmış hâlde
// geçtiğini doğrular.
//
// Boş değerin nil'e dönmesi ŞEMAYI gezen bir testte sabitlenir (bkz.
// schema_test.go, TestBosMetinArgumaniSuzgecKurmaz); burada iddia edilen şey
// kırpmanın kendisidir: " tişört " ile "tişört" aynı aramadır ve ikisini ayrı
// sorgu saymak, sonucu istemcinin göremediği bir boşluğa bağlardı.
func TestMetinArgumanlariKirpilarakGecer(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc,
		`{ products(q: "  tişört  ") { count } }`)

	require.Empty(t, yanit.Errors)

	opts := svc.sonListe(t)
	require.NotNil(t, opts.Search)
	assert.Equal(t, "tişört", *opts.Search)
}

// TestSayfalamaVarsayilaniServiseBirakilir verilmeyen limit'in 0 olarak
// geçtiğini doğrular.
//
// 0, servis için "varsayılanı uygula" demektir. Resolver'ın kendi varsayılanını
// seçmesi, aynı kuralın ikinci bir tanımı olurdu ve iki okuma yüzeyi farklı
// sayfa boyutları döndürmeye başlardı.
func TestSayfalamaVarsayilaniServiseBirakilir(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc, `{ products { count } }`)

	require.Empty(t, yanit.Errors)

	opts := svc.sonListe(t)
	assert.Zero(t, opts.Limit)
	assert.Zero(t, opts.Offset)
}

// TestTekilSorguIkiSeciciyiReddeder id ile handle'ın birlikte verilemeyeceğini
// doğrular.
//
// Birine öncelik vermek, çelişkili bir isteği sessizce yorumlamak olurdu:
// istemci handle'ı sorduğunu sanırken kimliğin yanıtını alırdı.
func TestTekilSorguIkiSeciciyiReddeder(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc,
		`{ product(id: "prod_1", handle: "tisort") { id } }`)

	require.NotEmpty(t, yanit.Errors)
	assert.Equal(t, "product_graphql_bad_argument", yanit.Errors[0].Extensions["code"])
	assert.Empty(t, svc.tekilSecici, "geçersiz istek servise ulaşmamalı")
}

// TestTekilSorguSeciciSizReddeder id de handle da verilmeyen sorgunun
// reddedildiğini doğrular.
func TestTekilSorguSeciciSizReddeder(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc, `{ product { id } }`)

	require.NotEmpty(t, yanit.Errors)
	assert.Equal(t, "product_graphql_bad_argument", yanit.Errors[0].Extensions["code"])
	assert.Empty(t, svc.tekilSecici)
}

// TestFiyatVeStokGevsekTipliDoner başka modüllerin kayıtlarının şemada JSON
// olarak taşındığını doğrular.
//
// Alanları tiplemek pricing/inventory şemasını bu modüle kopyalamak olurdu;
// kayıt buraya zaten gevşek tipli geliyor (bkz. service.StoreVariant).
func TestFiyatVeStokGevsekTipliDoner(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{tekil: service.StoreProduct{
		Product: models.Product{ID: "prod_1", Handle: "tisort"},
		Variants: []service.StoreVariant{{
			Variant:       models.Variant{ID: "var_1", ProductID: "prod_1", Title: "S"},
			PriceSet:      query.Record{"id": "pset_1", "amount": 1990},
			InventoryItem: query.Record{"id": "iitem_1", "stocked_quantity": 7},
		}},
	}}

	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc,
		`{ product(id: "prod_1") { variants { id priceSet inventoryItem } } }`)

	require.Empty(t, yanit.Errors)

	varyantlar := urunVaryantlari(t, yanit)
	require.Len(t, varyantlar, 1)

	varyant, ok := varyantlar[0].(map[string]any)
	require.True(t, ok)

	fiyat, ok := varyant["priceSet"].(map[string]any)
	require.True(t, ok, "fiyat seti nesne olarak dönmeli")
	assert.InDelta(t, float64(1990), fiyat["amount"], 0)

	stok, ok := varyant["inventoryItem"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "iitem_1", stok["id"])
}

// TestFiyatSaglayicisiYokkenNullDoner eksik kaydın null döndüğünü doğrular.
//
// pricing bu kurulumda kayıtlı değilse servis alanı hiç doldurmaz. Şemanın
// alanı null'lanabilir olmasının sebebi budur: tipli ve zorunlu bir alan,
// "fiyatı sıfır olan ürün" uydurmak zorunda kalırdı — yanlış fiyat
// göstermektense fiyatı hiç göstermemek yeğdir.
func TestFiyatSaglayicisiYokkenNullDoner(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{tekil: service.StoreProduct{
		Product:  models.Product{ID: "prod_1"},
		Variants: []service.StoreVariant{{Variant: models.Variant{ID: "var_1"}}},
	}}

	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc,
		`{ product(id: "prod_1") { variants { priceSet } } }`)

	require.Empty(t, yanit.Errors)

	varyant, ok := urunVaryantlari(t, yanit)[0].(map[string]any)
	require.True(t, ok)
	assert.Nil(t, varyant["priceSet"])
}

// TestServisHatasiTipiyleDoner servis hatasının sınıfını ve kodunu koruduğunu
// doğrular.
func TestServisHatasiTipiyleDoner(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{hata: coreerrors.NotFound("product_not_found", "ürün bulunamadı: prod_yok")}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc, `{ product(id: "prod_yok") { id } }`)

	require.NotEmpty(t, yanit.Errors)
	assert.Equal(t, "ürün bulunamadı: prod_yok", yanit.Errors[0].Message)
	assert.Equal(t, "product_not_found", yanit.Errors[0].Extensions["code"])
	assert.Nil(t, yanit.Data["product"], "bulunamayan ürün null döner")
}

// urunVaryantlari tekil sorgu yanıtındaki varyant dizisini döner.
func urunVaryantlari(t *testing.T, yanit graphqlYaniti) []any {
	t.Helper()

	urun, ok := yanit.Data["product"].(map[string]any)
	require.True(t, ok, "yanıtta ürün olmalı: %#v", yanit.Data)

	varyantlar, ok := urun["variants"].([]any)
	require.True(t, ok, "üründe varyant dizisi olmalı: %#v", urun)

	return varyantlar
}

// ptr verilen değerin adresini döner.
//
// Zarftaki sayaç işaretçidir (nil "sayılmadı" demektir, bkz.
// service.ListResult) ve sahte vitrinin döndürdüğü sabitler bu yüzden
// adreslenmek zorundadır.
func ptr[T any](v T) *T { return &v }

// TestSecilmeyenSayacHesaplanmaz seçim kümesinin İŞ MİKTARINI belirlediğini
// doğrular.
//
// Kapanan açık şuydu: "count" GraphQL'de bir alandır ve seçilmediğinde yanıtta
// zaten görünmezdi, ama sorgu yine de koşuyordu. gobit_load'da ölçüldü —
// 52.004 ürünlük katalogda sayaç 64,07 ms, isteğin geri kalanı 0,65 ms; yani
// istemcinin HİÇ İSTEMEDİĞİ bir alan isteğin %99'unu yazıyordu.
//
// İddia yanıta değil SERVİSE geçirilen ölçüte bakar: yanıtta "count" zaten
// yoktur (istemci seçmedi) ve ona bakan bir test, sayaç hesaplanmaya devam
// etse bile geçerdi.
func TestSecilmeyenSayacHesaplanmaz(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc,
		`{ products { items { id } } }`)

	require.Empty(t, yanit.Errors)
	assert.True(t, svc.sonListe(t).SkipCount,
		"seçilmeyen count için sayaç sorgusu istenmemeli")
}

// TestSecilenSayacHesaplanir alanın seçildiği durumda sayacın hâlâ
// istendiğini ve DOLU döndüğünü doğrular.
//
// [TestSecilmeyenSayacHesaplanmaz] tek başına, sayacı hiçbir zaman istemeyen
// bozuk bir uygulamayla da geçerdi. İki testin birlikte söylediği şey
// koşulun kendisidir: iş, alan istendiğinde yapılır.
//
// Şemadaki "count: Int!" nil kabul etmez, yani bu test aynı zamanda sözleşme
// ihlalinin kanıtıdır: sayaç seçilip de hesaplanmasaydı yanıt alan hatasıyla
// dönerdi.
func TestSecilenSayacHesaplanir(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{liste: service.ListResult[service.StoreProduct]{
		Items: []service.StoreProduct{{Product: models.Product{ID: "prod_1"}}},
		Count: ptr(42),
	}}

	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc,
		`{ products { count items { id } } }`)

	require.Empty(t, yanit.Errors)
	assert.False(t, svc.sonListe(t).SkipCount, "seçilen count hesaplanmalı")

	liste, ok := yanit.Data["products"].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, float64(42), liste["count"], 0)
}

// TestSkipEdilenSayacHesaplanmaz kararın @skip yönergesini de dinlediğini
// doğrular.
//
// Yönerge sunucu tarafında UYGULANIR: `count @skip(if: true)` yazan istemci o
// alanı yanıtta göremez, dolayısıyla onun için iş yapmak da yine boşa iştir.
// Kendi seçim kümesini elle gezen bir uygulama bu durumu kaçırırdı; gqlgen'in
// FieldRequested'ı kaçırmıyor ve bu test o farkı sabitler.
func TestSkipEdilenSayacHesaplanmaz(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc,
		`{ products { count @skip(if: true) items { id } } }`)

	require.Empty(t, yanit.Errors)
	assert.True(t, svc.sonListe(t).SkipCount,
		"@skip ile atlanan alan için sayaç sorgusu istenmemeli")

	liste, ok := yanit.Data["products"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, liste, "count", "atlanan alan yanıtta da olmamalı")
}

// TestFragmandakiSayacHesaplanir alanın bir FRAGMAN üzerinden istenmesinin de
// sayılmasını sağladığını doğrular.
//
// Üretilmiş istemciler alanları neredeyse her zaman fragman içinde ister.
// Yalnızca doğrudan seçimlere bakan bir uygulama burada sessizce yanlış cevap
// verirdi: sayaç hiç hesaplanmaz, şema "Int!" dediği için de yanıt alan
// hatasıyla düşerdi — yani üretilmiş her istemci kırılırdı.
func TestFragmandakiSayacHesaplanir(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{liste: service.ListResult[service.StoreProduct]{
		Items: []service.StoreProduct{{Product: models.Product{ID: "prod_1"}}},
		Count: ptr(3),
	}}

	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc,
		`{ products { ...sayfa items { id } } } fragment sayfa on ProductList { count }`)

	require.Empty(t, yanit.Errors, "%#v", yanit.Errors)
	assert.False(t, svc.sonListe(t).SkipCount,
		"fragman içinden istenen alan da SEÇİLMİŞTİR")

	liste, ok := yanit.Data["products"].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, float64(3), liste["count"], 0)
}
