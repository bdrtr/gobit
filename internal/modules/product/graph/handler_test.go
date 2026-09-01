package graph_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// olcumKatalogu kalibrasyonun BAYT ölçümleri için gerçekçi bir katalog üretir.
//
// Karmaşıklık kalibrasyonu (limits_test.go) alan SAYAR ve bunun için veriye
// ihtiyacı yoktur; bayt kalibrasyonu içerik olmadan hiçbir şey ölçemez.
// Ürünün ağırlığı gerçek bir katalogdan alınmıştır: 4 KiB'lık bir açıklama,
// üç varyant (fiyat ve stok kayıtlarıyla), seçenekler, görseller, etiketler ve
// kategoriler.
//
// Ölçüm ANCAK bu fikstürle anlamlıdır ve değiştirilirse [TestYanitBaytKalibrasyonu]
// başka bir şey ölçmeye başlar; sayıları README'deki tabloyla birlikte
// güncellenmelidir.
func olcumKatalogu(adet int) service.ListResult[service.StoreProduct] {
	aciklama := strings.Repeat("Pamuklu bisiklet yaka tişört. ", 137)
	kisa := "Yazlık koleksiyon"
	an := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)

	urunler := make([]service.StoreProduct, 0, adet)

	for i := range adet {
		kimlik := "prod_" + strconv.Itoa(i)

		varyantlar := make([]service.StoreVariant, 0, 3)
		for v := range 3 {
			varyantlar = append(varyantlar, service.StoreVariant{
				Variant: models.Variant{
					ID: kimlik + "_var_" + strconv.Itoa(v), ProductID: kimlik,
					Title: "S", SKU: &kisa, Rank: int32(v), CreatedAt: an, UpdatedAt: an,
					OptionValues: []models.OptionValue{
						{ID: "optval_1", OptionID: "opt_1", Value: "S"},
						{ID: "optval_2", OptionID: "opt_2", Value: "Kırmızı"},
					},
				},
				PriceSet:      query.Record{"id": "pset_1", "amount": 19990, "currency": "TRY"},
				InventoryItem: query.Record{"id": "iitem_1", "stocked_quantity": 42},
			})
		}

		urunler = append(urunler, service.StoreProduct{
			Product: models.Product{
				ID: kimlik, Handle: "tisort-" + strconv.Itoa(i), Title: "Bisiklet Yaka Tişört",
				Subtitle: &kisa, Description: &aciklama, Thumbnail: &kisa,
				Metadata: map[string]any{"renk": "kırmızı"}, CreatedAt: an, UpdatedAt: an,
				Options: []models.Option{{ID: "opt_1", ProductID: kimlik, Title: "Beden",
					Values: []models.OptionValue{{ID: "optval_1", OptionID: "opt_1", Value: "S"}}}},
				Images: []models.Image{{ID: "img_1", ProductID: kimlik,
					URL: "https://cdn.example.com/1.jpg"}},
				Tags:       []models.Tag{{ID: "tag_1", Value: "yeni"}},
				Categories: []models.Category{{ID: "pcat_1", Name: "Tişört", Handle: "tisort"}},
			},
			Variants: varyantlar,
		})
	}

	return service.ListResult[service.StoreProduct]{
		Items: urunler, Count: 5000, Offset: 0, Limit: adet,
	}
}

// logYakalayici sunucunun yazdığı log satırlarını belleğe alır.
//
// Maskeleme iddiasının İKİ yarısı vardır ve tek başına birincisi eksiktir:
// hatanın istemciye gitmemesi, hatanın KAYBOLMASI demek olmamalıdır. Operatör
// gerçek metni bir yerde görmezse maskeleme bir sızıntıyı değil bir arızayı
// gizler.
//
// Kilit gerçek bir ihtiyaçtır: gqlgen kök alanları eşzamanlı çözer, yani tek
// istekte birden fazla gorutin log yazabilir.
type logYakalayici struct {
	mu    sync.Mutex
	kayit bytes.Buffer
}

// Write log satırını belleğe alır.
func (l *logYakalayici) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.kayit.Write(p)
}

// metin o ana kadar yazılmış log satırlarını döner.
func (l *logYakalayici) metin() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.kayit.String()
}

// logluKimlik logları yakalanan bir mağaza kimliği bağlamı kurar.
//
// Logger context'e KONUR çünkü çekirdek onu oradan okur (corehttp.WriteError →
// LoggerFromContext); global logger'ı değiştirmek testleri paralel
// koşulamaz hâle getirirdi.
func logluKimlik(kanallar []string) (context.Context, *logYakalayici) {
	yakalayici := &logYakalayici{}
	ctx := corehttp.WithLogger(kimlikli(kanallar),
		slog.New(slog.NewTextHandler(yakalayici, nil)))

	return ctx, yakalayici
}

// restZarfi aynı hatanın REST yüzeyinde ürettiği gövdeyi döner.
func restZarfi(t *testing.T, err error) corehttp.ErrorResponse {
	t.Helper()

	rec := httptest.NewRecorder()
	corehttp.WriteError(context.Background(), rec, err)

	var zarf corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &zarf))

	return zarf
}

// TestIcHataIstemciyeSizmaz ikinci okuma yüzeyinin, birincisinin gizlediği
// sunucu içi ayrıntıyı açmadığını doğrular.
//
// Bu test bu modülün var oluş sebebini sınar: "hangi hata istemciye olduğu
// gibi verilebilir" kuralı çekirdekte TEK bir yerde tanımlıdır ve ikinci bir
// yüzeyin onu kendi başına uygulaması, ayrıştığı gün sızıntı demektir.
func TestIcHataIstemciyeSizmaz(t *testing.T) {
	t.Parallel()

	gizli := coreerrors.Internal("product_db_down",
		"bağlantı düştü: postgres://gobit:parola@10.0.0.7:5432/gobit")

	svc := &sahteVitrin{hata: gizli}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc, `{ products { count } }`)

	require.NotEmpty(t, yanit.Errors)

	mesaj := yanit.Errors[0].Message
	assert.NotContains(t, mesaj, "postgres://", "bağlantı dizesi istemciye sızmamalı")
	assert.NotContains(t, mesaj, "parola")

	// İddianın asıl gücü burada: mesaj REST yüzeyinin AYNI hata için yazdığı
	// mesajın ta kendisidir. Metni buraya sabit yazmak, çekirdek maskeleme
	// metnini değiştirdiğinde testi haklı çıkarır ama yüzeyi ayrıştırırdı.
	zarf := restZarfi(t, gizli)
	assert.Equal(t, zarf.Error.Message, mesaj)
	assert.Equal(t, zarf.Error.Code, yanit.Errors[0].Extensions["code"])
}

// TestIcHataninMetniYanitinHicbirYerindeGecmez maskelemeyi çözülmüş yanıt
// üzerinden değil HAM BAYTLAR üzerinden sınar.
//
// [TestIcHataIstemciyeSizmaz] yalnızca errors[0].message alanına bakar; oysa
// GraphQL yanıtının sızıntı için birden fazla yeri vardır: extensions,
// path, ve gqlerror'ın sarmaladığı hatanın kütüphane tarafından bir gün
// serileştirilmeye başlanması. Bu testin iddiası daha basit ve daha güçlüdür:
// metin yanıtın HİÇBİR yerinde geçmemeli.
//
// Sızabilecek şeyler somut: bağlantı dizesi (kullanıcı adı ve parolayla),
// tablo ve sütun adları, dosya yolları, sorgu metni. Hepsi çekirdeğin
// kindPolicy'sinde maskelenir; ikinci okuma yüzeyi aynı politikayı kendi
// başına uygulamadığı SÜRECE (bkz. hataSunucusu) burada da maskeli kalır.
func TestIcHataninMetniYanitinHicbirYerindeGecmez(t *testing.T) {
	t.Parallel()

	const sir = "postgres://gobit:parola@10.0.0.7:5432/gobit"

	gizli := coreerrors.Internal("product_db_down",
		"bağlantı düştü: "+sir+" (tablo: products, sorgu: SELECT * FROM products)")

	svc := &sahteVitrin{hata: gizli}
	rec := istekYap(t, kimlikli([]string{"sc_1"}), svc, `{ products { count } }`, graph.Options{})

	govde := rec.Body.String()
	require.Contains(t, govde, "errors", "hata gerçekten dönmüş olmalı: %s", govde)

	for _, sizinti := range []string{sir, "parola", "SELECT", "products,", "10.0.0.7"} {
		assert.NotContains(t, govde, sizinti,
			"iç hatanın metni yanıtın hiçbir yerinde geçmemeli: %s", govde)
	}
}

// TestIstemciHatasiOlduguGibiDoner istemciye ait hataların maskelenmediğini
// doğrular.
//
// Maskeleme yalnızca sunucu hatalarına uygulanır; doğrulama hatasını
// maskelemek istemcinin sorgusunu düzeltmesini imkânsız kılardı.
func TestIstemciHatasiOlduguGibiDoner(t *testing.T) {
	t.Parallel()

	acik := coreerrors.Invalid("product_bad_query_param", "limit negatif olamaz (verilen: -1)")

	svc := &sahteVitrin{hata: acik}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc, `{ products { count } }`)

	require.NotEmpty(t, yanit.Errors)

	zarf := restZarfi(t, acik)
	assert.Equal(t, zarf.Error.Message, yanit.Errors[0].Message)
	assert.Equal(t, "product_bad_query_param", yanit.Errors[0].Extensions["code"])
}

// TestTipsizHataMaskelenirVeLoglanir sınıflandırılmamış hatanın da
// maskelendiğini ve KAYBOLMADIĞINI doğrular.
//
// Açık olan dal buydu: presenter "*coreerrors.Error mi" diye soruyor, olmayanı
// olduğu gibi geçiriyordu. Oysa vitrin servisinin altındaki her katman tipsiz
// hata döndürebilir — sürücünün hatası bunların en yaygınıdır ve tam da en
// zararlısıdır: pq'nun mesajı bağlantı dizesini, kullanıcı adını, PAROLAYI ve
// çalıştırılan SQL'i taşır. Ölçüldü, aynen istemciye gidiyordu (durum 200) ve
// hiçbir yere de yazılmıyordu.
//
// Çekirdeğin kuralı tam tersidir ve ikinci bir tanım yazılmadan uygulanır:
// tipsiz hata KindInternal sayılır, mesajı genel metinle DEĞİŞTİRİLİR ve
// gerçek hata loglanır. Test iddianın üç yarısını da tutar — sızmadı,
// REST'le aynı şeyi söyledi, loga düştü.
func TestTipsizHataMaskelenirVeLoglanir(t *testing.T) {
	t.Parallel()

	const dsn = "pq: SSL connection error host=db.internal user=gobit " +
		"password=s3cr3t dbname=gobit; SELECT * FROM product_products WHERE id=$1"

	ham := errors.New(dsn)

	ctx, loglar := logluKimlik([]string{"sc_1"})
	rec := istekYap(t, ctx, &sahteVitrin{hata: ham}, `{ products { count } }`, graph.Options{})

	govde := rec.Body.String()
	for _, sizinti := range []string{"password", "s3cr3t", "db.internal", "SELECT", "product_products"} {
		assert.NotContains(t, govde, sizinti,
			"tipsiz hatanın metni yanıtta geçmemeli: %s", govde)
	}

	var yanit graphqlYaniti
	require.NoError(t, json.Unmarshal([]byte(govde), &yanit), "gövde: %s", govde)
	require.NotEmpty(t, yanit.Errors)

	// Metin buraya sabit yazılmaz: iddia "REST ne diyorsa aynısı" olmalı.
	zarf := restZarfi(t, ham)
	assert.Equal(t, zarf.Error.Message, yanit.Errors[0].Message)
	assert.Equal(t, zarf.Error.Code, yanit.Errors[0].Extensions["code"])

	assert.Contains(t, loglar.metin(), "s3cr3t",
		"maskelenen hata operatör için loglanmalı; yoksa maskeleme arızayı gizler")
}

// TestSarmalanmisTipsizHataDaMaskelenir maskelemenin zincirin en üstüne değil
// SINIFA baktığını doğrular.
//
// Servis hataları nadiren çıplak gelir; ara katmanlar onları fmt.Errorf ile
// sarar ve sarmalayıcı da tipsizdir. Sınıflandırma sarmalanmış hatayı da
// KindInternal saymazsa, tek bir %w ile maskeleme atlatılırdı.
func TestSarmalanmisTipsizHataDaMaskelenir(t *testing.T) {
	t.Parallel()

	sarmali := fmt.Errorf("katalog okunamadı: %w",
		errors.New("pq: password=s3cr3t host=db.internal"))

	ctx, loglar := logluKimlik([]string{"sc_1"})
	rec := istekYap(t, ctx, &sahteVitrin{hata: sarmali}, `{ products { count } }`, graph.Options{})

	govde := rec.Body.String()
	assert.NotContains(t, govde, "s3cr3t")
	assert.NotContains(t, govde, "katalog okunamadı")
	assert.Contains(t, loglar.metin(), "s3cr3t")
}

// TestBelgeHatasiSunucuLoguKirletmez protokol hatalarının sunucu hatası
// SAYILMADIĞINI doğrular.
//
// İddia maskelemenin öteki yarısıdır: her hatayı çekirdeğe vermek, istemcinin
// yazım yanlışını ERROR seviyesinde bir sunucu arızası gibi kaydettirirdi ve
// yüzey açık olduğu için o satırları yazan istemci olurdu. Ayrım kaynağa
// bakar; bu test kaynağın gerçekten ayrıldığını gösterir.
func TestBelgeHatasiSunucuLoguKirletmez(t *testing.T) {
	t.Parallel()

	ctx, loglar := logluKimlik([]string{"sc_1"})
	rec := istekYap(t, ctx, &sahteVitrin{}, `{ products { olmayanAlan } }`, graph.Options{})

	assert.Contains(t, rec.Body.String(), "olmayanAlan", "doğrulama hatası maskelenmemeli")
	assert.Empty(t, loglar.metin(), "istemcinin sorgu hatası sunucu hatası olarak loglanmamalı")
}

// TestSorguHatasiMaskelenmez GraphQL'in KENDİ hatalarının olduğu gibi
// döndüğünü doğrular.
//
// Sorgu ayrıştırma ve şema doğrulama hataları istemcinin YAZDIĞI belgeyle
// ilgilidir; maskelenirlerse yüzey hata ayıklanamaz hâle gelir — "Cannot query
// field" yerine "sunucu hatası" gören istemci sorgusunu düzeltemez.
func TestSorguHatasiMaskelenmez(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc, `{ products { olmayanAlan } }`)

	require.NotEmpty(t, yanit.Errors)
	assert.Contains(t, yanit.Errors[0].Message, "olmayanAlan")
}

// TestGETIleSorguKabulEdilmez uca yalnızca POST taşımasının bağlandığını
// doğrular.
//
// Karar ve gerekçesi [graph.NewHandler] belgesinde: yanıt satış kanalına göre
// değiştiği için GET'in önbellek getirisi burada yoktur, bedelleri ise
// gerçektir (sorgunun loglara ve tarayıcı geçmişine düşmesi, URL uzunluğu).
func TestGETIleSorguKabulEdilmez(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}

	req := httptest.NewRequest(http.MethodGet, graph.Path+"?query={products{count}}", http.NoBody)
	rec := httptest.NewRecorder()
	graph.NewHandler(svc, graph.Options{}).ServeHTTP(rec, req)

	assert.NotEqual(t, http.StatusOK, rec.Code)
	assert.Empty(t, svc.listeOlculeri, "GET ile gelen sorgu çalıştırılmamalı")
}

// TestYanitBaytKalibrasyonu sertleştirmenin hiç sormadığı boyutu ölçer.
//
// Kalibrasyon tablosu bugüne kadar yalnızca ALAN SAYIMINI ölçüyordu; oysa
// kaçırdığı şey tam olarak bayttı. Bu test tabloya bayt sütununu getiren
// ölçümdür ve iki yönlü iddia eder:
//
//   - Bugünkü tavanlardan geçen EN AĞIR meşru belge gerçekten ağır olmalı
//     (aksi hâlde ölçüm boş bir katalogu ölçer ve "sınır bol" demek anlamsız
//     olur).
//   - Aynı yanıt varsayılan bayt sınırının çok altında kalmalı; [graph.Options]
//     bir gün daraltılırsa vitrinin kendi sorgusu kırılmadan önce bu test
//     patlar.
func TestYanitBaytKalibrasyonu(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{liste: olcumKatalogu(20)}
	belge := `{ products { count offset limit items {` + tumUrunAlanlari + `} } }`

	rec := istekYap(t, kimlikli([]string{"sc_1"}), svc, belge, graph.Options{})

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), `"errors"`, "en ağır meşru belge geçmeli")

	bayt := rec.Body.Len()
	assert.Greater(t, bayt, 100<<10, "ölçüm gerçekten ağır bir yanıtı ölçmeli")
	assert.Less(t, bayt, graph.DefaultMaxResponseBytes/8,
		"en ağır meşru yanıt, bayt sınırının çok altında kalmalı")
}

// TestYanitBoyutuSiniriTamZarfDoner sınıra çarpan yanıtın YARIM
// GÖNDERİLMEDİĞİNİ doğrular.
//
// Karar buydu: gövdenin hiçbir baytı gitmemişken sınır aşılırsa aşan gövde
// atılır ve yerine TAM, geçerli bir hata zarfı yazılır. Yarım JSON
// göndermek istemciyi bozardı — ya ayrıştırma hatası alır ve sebebini
// bilemez, ya da kırpılmış gövdeyi kısa bir sonuç sanardı.
//
// İddianın gücü "geçerli JSON" kısmındadır: kırpılmış bir gövde de "errors"
// içermeyebilir ama çözülemezdi.
func TestYanitBoyutuSiniriTamZarfDoner(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{liste: olcumKatalogu(20)}
	belge := `{ products { items {` + tumUrunAlanlari + `} } }`

	rec := istekYap(t, kimlikli([]string{"sc_1"}), svc, belge,
		graph.Options{MaxResponseBytes: 4 << 10})

	govde := rec.Body.String()
	assert.Less(t, len(govde), 4<<10, "sınırı aşan gövde istemciye gitmemeli")
	assert.NotContains(t, govde, "Pamuklu", "kırpılmış katalog verisi sızmamalı")

	var yanit graphqlYaniti
	require.NoError(t, json.Unmarshal([]byte(govde), &yanit),
		"yanıt yarım değil, çözülebilir olmalı: %s", govde)

	require.NotEmpty(t, yanit.Errors)
	assert.Equal(t, "RESPONSE_LIMIT_EXCEEDED", yanit.Errors[0].Extensions["code"])
	assert.Contains(t, yanit.Errors[0].Message, "exceeds the limit")
}

// TestYanitBoyutuSiniriMesruYanitiGecirir kapının her yanıtı reddetmediğini
// doğrular.
//
// "Aşınca reddet" testi tek başına eksiktir: gövdeyi hiç yazmayan bir
// sarmalayıcı da onu geçerdi. Sayımın nerede bittiği ancak sınırın hemen
// altındaki yanıt geçince belli olur.
func TestYanitBoyutuSiniriMesruYanitiGecirir(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{liste: olcumKatalogu(1)}
	rec := istekYap(t, kimlikli([]string{"sc_1"}), svc,
		`{ products { items { id handle title } } }`, graph.Options{MaxResponseBytes: 4 << 10})

	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), `"errors"`)
	assert.Contains(t, rec.Body.String(), "tisort-0")
}

// TestJetonSiniriBelgeyiAyristirirkenKeser gövde sınırının altında kalan ama
// binlerce jeton taşıyan belgenin AYRIŞTIRILIRKEN kesildiğini doğrular.
//
// Ölçüldü: 302 takma adlı __schema belgesi 45.796 bayttır, yani 64 KiB'lık
// gövde kapısından rahatça geçer; jetonu ise 9.364'tür. Kapı en ucuz olandır —
// belge sonuna kadar ayrıştırılmadan reddedilir, dolayısıyla ondan sonraki
// hiçbir kapının çalışması gerekmez.
func TestJetonSiniriBelgeyiAyristirirkenKeser(t *testing.T) {
	t.Parallel()

	const altAgac = `__schema{types{name kind description fields{name description ` +
		`args{name description}} inputFields{name description} enumValues{name description}}}`

	belge := "{" + takmaAdliAlan(302, altAgac) + "}"
	require.Less(t, len(belge), 64<<10, "belge gövde kapısından geçecek kadar küçük olmalı")

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc, belge)

	require.NotEmpty(t, yanit.Errors)
	assert.Contains(t, yanit.Errors[0].Message, "token limit")
	assert.Empty(t, svc.listeOlculeri)
}

// TestJetonSiniriMesruBelgeyiGecirir jeton tavanının vitrinin gerçek
// belgelerine dokunmadığını doğrular.
//
// Ölçüldü: en ağır meşru sorgu 95 jetondur, on kök sorgulu fragment ağırlıklı
// bir belge 922. Tavan 8.192; yani sınır meşru kullanımın yaklaşık dokuz
// katıdır. Bu test o payı korur — daraltan biri, neyi feda ettiğini burada
// görür.
func TestJetonSiniriMesruBelgeyiGecirir(t *testing.T) {
	t.Parallel()

	belge := "{" + takmaAdliAlan(10, `products(limit: 2) { items {`+tumUrunAlanlari+`} }`) + "}"

	svc := &sahteVitrin{}
	yanit, _ := sorgulaOpts(t, kimlikli([]string{"sc_1"}), svc, belge,
		graph.Options{MaxComplexity: 1 << 20})

	require.Empty(t, yanit.Errors, "fragment ağırlıklı meşru belge jeton sınırına takılmamalı")
	assert.Len(t, svc.listeOlculeri, 10)
}

// TestIcGozlemKapaliykenSemaAdlariOnerilmez anahtarın vaat ettiği şeyi
// GERÇEKTEN yaptığını doğrular.
//
// Anahtar bir zamanlar yalnızca __schema'yı kapatıyordu ve şemayı hiç
// gizlemiyordu: doğrulayıcı, iç gözlem kapalıyken de adları perakende
// dağıtıyordu. Ölçüldü — dört ayrı kural, dört ayrı sızıntı: yanlış yazılmış
// alan, yanlış yazılmış tip, yanlış yazılmış argüman ve seçimsiz bırakılmış
// alan. Doğrulayıcı bir belgedeki bütün hataları TEK yanıtta topladığı için
// bir istekte onlarca ad denenebiliyordu ve hız sınırı buna engel değildi.
//
// İddia "Did you mean" üzerinedir çünkü sızıntının aracı odur: teşhis cümlesi
// (hangi alan yanlış) istemcinin kendi yazdığını tekrarlar, ADLARI SAYAN
// cümle ise şemayı okur.
func TestIcGozlemKapaliykenSemaAdlariOnerilmez(t *testing.T) {
	t.Parallel()

	kapali := graph.Options{IntrospectionDisabled: true}

	belgeler := map[string]string{
		"bilinmeyen alan":     `{ prodcts { count } }`,
		"bilinmeyen alt alan": `{ products { itemz { id } } }`,
		"seçimsiz alan":       `{ products { items } }`,
		"bilinmeyen tip":      `fragment f on Prodct { id } { products { items { ...f } } }`,
		"bilinmeyen argüman":  `{ products(limitt: 3) { count } }`,
	}

	for ad, belge := range belgeler {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			rec := istekYap(t, kimlikli([]string{"sc_1"}), &sahteVitrin{}, belge, kapali)

			govde := rec.Body.String()
			require.Contains(t, govde, "errors", "belge gerçekten reddedilmeli: %s", govde)
			assert.NotContains(t, govde, "Did you mean",
				"iç gözlem kapalıyken şemanın adları sayılmamalı: %s", govde)
		})
	}

	// Tek bir somut ad üzerinden ikinci bir iddia: "products" adı, onu yanlış
	// yazan istemciye geri okunmamalı.
	rec := istekYap(t, kimlikli([]string{"sc_1"}), &sahteVitrin{}, `{ prodcts { count } }`, kapali)
	assert.NotContains(t, rec.Body.String(), "products")
}

// TestIcGozlemAcikkenOneriKorunur kapatmanın bedava OLMADIĞINI doğrular.
//
// Öneriler açık yüzeyde gerçek bir geliştirici kolaylığıdır ve varsayılan
// AÇIK olduğu için bugün korunur. Test bu yüzden vardır: yukarıdaki iddia,
// önerileri her koşulda söken bir düzeltmeyle de geçerdi — ve o düzeltme
// vitrin geliştiricisinin yazım yanlışını sessizce pahalılaştırırdı.
func TestIcGozlemAcikkenOneriKorunur(t *testing.T) {
	t.Parallel()

	yanit, _ := sorgulaOpts(t, kimlikli([]string{"sc_1"}), &sahteVitrin{},
		`{ prodcts { count } }`, graph.Options{})

	require.NotEmpty(t, yanit.Errors)
	assert.Contains(t, yanit.Errors[0].Message, `Did you mean "products"`)
}

// TestIcGozlemKapaliykenBelgeCalistirilmaz iç gözlem sorgusunun kendi kapımızda
// durdurulduğunu doğrular.
//
// Sebep hata politikasıdır: gqlgen iç gözlemi ÇALIŞTIRMA anında düz bir
// errors.New ile reddeder ve o hata, resolver'ın döndürdüğü tipsiz hatadan
// ayırt edilemez — yani [graph.NewHandler]'ın maskeleme kuralı onu haklı
// olarak sunucu hatası sayar ve her deneme bir ERROR satırı yazardı. Belgeyi
// kapıda reddetmek hem doğru mesajı verir hem de yüzeyi bir log borusu
// olmaktan çıkarır.
func TestIcGozlemKapaliykenBelgeCalistirilmaz(t *testing.T) {
	t.Parallel()

	ctx, loglar := logluKimlik([]string{"sc_1"})
	rec := istekYap(t, ctx, &sahteVitrin{}, `{ __schema { queryType { name } } }`,
		graph.Options{IntrospectionDisabled: true})

	var yanit graphqlYaniti
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &yanit), "gövde: %s", rec.Body.String())

	require.NotEmpty(t, yanit.Errors)
	assert.Equal(t, "INTROSPECTION_DISABLED", yanit.Errors[0].Extensions["code"])
	assert.Nil(t, yanit.Data, "belge hiç çalıştırılmamalı")
	assert.Empty(t, loglar.metin(), "iç gözlem denemesi sunucu hatası olarak loglanmamalı")
}

// TestIcGozlemKapaliykenTypenameCalisir kapının fazlasını kapatmadığını
// doğrular.
//
// __typename bir iç gözlem kökü DEĞİLDİR: her tipte bulunan ve tek bir dize
// döndüren bir yapraktır ve normalize eden istemciler (Apollo, urql) onu
// kendileri ekler. Kotadan saymak, iç gözlemi kapatan kurulumda bu
// istemcilerin her sorgusunu kırardı.
func TestIcGozlemKapaliykenTypenameCalisir(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgulaOpts(t, kimlikli([]string{"sc_1"}), svc,
		`{ __typename products { count } }`, graph.Options{IntrospectionDisabled: true})

	require.Empty(t, yanit.Errors)
	assert.Equal(t, "Query", yanit.Data["__typename"])
	assert.Len(t, svc.listeOlculeri, 1)
}

// TestBozukJSONGovdeyiYansitmaz çözülemeyen gövdenin yanıta GERİ
// DÖNMEDİĞİNİ doğrular.
//
// Ölçüldü: gqlgen'in POST taşıması JSON'u çözemediğinde HAM GÖVDEYİ hata
// mesajına ekliyor (transport/http_post.go) ve o mesaj tipsiz olduğu için
// olduğu gibi geçiyordu. Yani 64 KiB'a kadar saldırgan denetimindeki metin
// hem yanıta hem de yanıtı kaydeden ara katmanların loglarına giriyordu —
// XSS değil (Content-Type JSON) ama yansıtma ve log kirletme.
//
// Kapı bir metin eşleşmesi değildir: taşımanın hatası KODSUZ gelir (gqlgen
// ayrıştırmadan itibaren her protokol hatasına kod koyar) ve kodsuz hatanın
// metnini biz yazarız.
func TestBozukJSONGovdeyiYansitmaz(t *testing.T) {
	t.Parallel()

	const gizli = "GIZLI_METIN_AAA"

	req := httptest.NewRequest(http.MethodPost, graph.Path,
		strings.NewReader(`{"query": `+gizli+`}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(kimlikli([]string{"sc_1"}))

	rec := httptest.NewRecorder()
	graph.NewHandler(&sahteVitrin{}, graph.Options{}).ServeHTTP(rec, req)

	govde := rec.Body.String()
	assert.NotContains(t, govde, gizli, "gövde yanıta yansımamalı: %s", govde)

	var yanit graphqlYaniti
	require.NoError(t, json.Unmarshal([]byte(govde), &yanit), "gövde: %s", govde)

	require.NotEmpty(t, yanit.Errors)
	assert.Equal(t, "REQUEST_DECODE_FAILED", yanit.Errors[0].Extensions["code"])
}
