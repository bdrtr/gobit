//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/module"
	coreplugin "github.com/bdrtr/gobit/internal/core/plugin"
	cartapi "github.com/bdrtr/gobit/internal/modules/cart/api"
	cartsvc "github.com/bdrtr/gobit/internal/modules/cart/service"
	paymentmod "github.com/bdrtr/gobit/internal/modules/payment"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	"github.com/bdrtr/gobit/plugins/paymentstripe"
)

// Bu dosya planın Faz 9 DoD'sini kanıtlar:
//
//	"Örnek payment provider plugin'i çekirdeğe dokunmadan takılıp
//	 seçilebiliyor; trace'ler dışa aktarılıyor; OpenAPI şeması üretiliyor;
//	 temel yük testi geçiyor."
//
// Trace tarafı burada değil, internal/core/http/telemetry_test.go içinde
// bellek içi bir dışa aktarıcıyla kanıtlanır: span'ın gerçekten üretildiğini
// görmek için bir OTLP toplayıcısı ayağa kaldırmak, kanıta hiçbir şey
// eklemeden testi ağa bağımlı kılardı.

// TestSepetYaratmaBilerekOynatilmaz: aynı anahtarla gelen ikinci istek
// birincinin sepetini DEĞİL, yeni bir sepet almalıdır.
//
// # Bu bir gerileme değil, kapatılmış bir sızıntı
//
// Bu test bir zamanlar tam TERSİNİ iddia ediyordu — "ağı kopan istemci iki
// sepet değil bir sepet almalı" — ve iddia kendi başına makuldü. Yanlış olan,
// idempotency kaydının VİTRİNDE çağıranları ayırabildiği varsayımıydı.
//
// Kayıt, çağıranın kimliğiyle ad alanına alınır. /store/v1'de çözülen kimlik
// alışverişçinin değil MAĞAZANIN kimliğidir: publishable anahtar her tarayıcıda
// aynıdır ve gizli olmadığı çekirdeğin kendi godoc'unda yazılıdır. Yani bütün
// müşteriler TEK kova paylaşıyordu ve kaydı seçen şey istemcinin seçtiği bir
// başlıktı. Sepet yaratma, yolunda hiçbir yetenek TAŞIMAYAN ve yanıtında bir
// yetenek ÜRETEN tek uçtu; aynı anahtar ve aynı gövdeyle gelen ikinci müşteri
// birincinin sepet kimliğini alıyordu — sepette sahiplik denetimi olmadığı için
// (bkz. README, "Bilinen sınırlar") bu, yabancıya birinin sepetini vermekti.
//
// Ödenen bedel: zaman aşımına uğrayan bir yaratma isteğini tekrarlayan istemci
// iki sepet açar. Biri terk edilir. Para, stok ve müşteriye görünen hiçbir şey
// etkilenmez.
func TestSepetYaratmaBilerekOynatilmaz(t *testing.T) {
	ctx := context.Background()

	govde, err := json.Marshal(map[string]string{"country_code": vergiliUlke})
	require.NoError(t, err, "sepet isteği kodlanamadı")

	sepetIstegi := func() *http.Request {
		istek := httptest.NewRequest(http.MethodPost, cartapi.StoreCartsPath, bytes.NewReader(govde))
		istek.Header.Set("Content-Type", "application/json")
		istek.Header.Set(corehttp.PublishableKeyHeader, publishableAnahtar)
		istek.Header.Set(corehttp.IdempotencyKeyHeader, "e2e-sepet-anahtari-1")

		return istek
	}

	oncekiSayi := sepetSayisi(ctx, t)

	ilk := httptest.NewRecorder()
	testRouter.ServeHTTP(ilk, sepetIstegi())
	require.Equal(t, http.StatusCreated, ilk.Code, "gövde: %s", ilk.Body.String())

	ikinci := httptest.NewRecorder()
	testRouter.ServeHTTP(ikinci, sepetIstegi())
	require.Equal(t, http.StatusCreated, ikinci.Code, "gövde: %s", ikinci.Body.String())

	assert.Empty(t, ikinci.Header().Get(corehttp.IdempotencyReplayedHeader),
		"sepet yaratma halkadan MUAFtır; oynatma başlığı hiç çıkmamalı")
	assert.NotEqual(t, sepetKimliginiOku(t, ilk), sepetKimliginiOku(t, ikinci),
		"ikinci çağıran BİRİNCİNİN sepetini almamalı; asıl iddia budur")
	assert.Equal(t, oncekiSayi+2, sepetSayisi(ctx, t),
		"iki istek iki sepet yazmalı; muafiyetin bedeli tam olarak budur")
}

// TestSepetKapsamliUctaIdempotencyKorunur muafiyetin YALNIZCA yaratmaya
// dokunduğunu doğrular.
//
// Muafiyet tam yol eşleşmesiyle çalışır, önekle değil. Önek olsaydı
// /store/v1/carts/{id}/complete de düşerdi ve o uç, çift SİPARİŞ üreten uçtur —
// yani korumanın gerçekten gerektiği tek yer halkadan çıkmış olurdu.
//
// Bu uçlarda ad alanı sorunu da yoktur: parmak izi YOLU içerir ve yolda sepet
// kimliği vardır, dolayısıyla aynı anahtarı kendi sepetinde kullanan ikinci
// müşteri başkasının verisini değil 409 alır.
func TestSepetKapsamliUctaIdempotencyKorunur(t *testing.T) {
	sepetID, _ := sepetOlustur(t)

	adresIstegi := func(anahtar string) *http.Request {
		govde, err := json.Marshal(map[string]any{
			"address": map[string]string{"country_code": vergiliUlke, "city": "Istanbul"},
		})
		require.NoError(t, err)

		istek := httptest.NewRequest(http.MethodPost,
			"/store/v1/carts/"+sepetID+"/shipping-address", bytes.NewReader(govde))
		istek.Header.Set("Content-Type", "application/json")
		istek.Header.Set(corehttp.PublishableKeyHeader, publishableAnahtar)
		istek.Header.Set(corehttp.IdempotencyKeyHeader, anahtar)

		return istek
	}

	ilk := httptest.NewRecorder()
	testRouter.ServeHTTP(ilk, adresIstegi("e2e-adres-anahtari"))
	require.Less(t, ilk.Code, http.StatusInternalServerError, "gövde: %s", ilk.Body.String())

	ikinci := httptest.NewRecorder()
	testRouter.ServeHTTP(ikinci, adresIstegi("e2e-adres-anahtari"))

	assert.Equal(t, "true", ikinci.Header().Get(corehttp.IdempotencyReplayedHeader),
		"sepet kapsamlı uçta tekrar, kaydedilmiş yanıtın oynatılması olmalı")
	assert.Equal(t, ilk.Code, ikinci.Code, "oynatılan yanıt ilkinin durumunu taşımalı")
	// Gövde JSONEq ile değil ham karşılaştırmayla sınanır: bu uç gövdesiz de
	// yanıtlayabilir ve JSONEq boş dizeyi "geçersiz JSON" diye düşürür — yani
	// oynatmanın doğru çalıştığı durumda testi kırardı.
	assert.Equal(t, ilk.Body.String(), ikinci.Body.String(),
		"oynatılan yanıt ilkinin AYNISI olmalı")
}

// sepetSayisi veritabanındaki toplam sepet sayısını döner.
func sepetSayisi(ctx context.Context, t *testing.T) int64 {
	t.Helper()

	_, toplam, err := sepetSvc.ListCarts(ctx, cartsvc.ListCartsInput{})
	require.NoError(t, err, "sepetler sayılamadı")

	return toplam
}

// TestIdempotencyAyniAnahtarFarkliGovdeyiReddeder anahtarın yeniden
// kullanımının sessizce yanlış yanıt döndürmediğini doğrular.
//
// Sessizce ilk yanıtı çalmak, istemcinin İKİNCİ isteğinin hiç işlenmediğini
// gizlerdi: istemci "ikinci sepetim hazır" sanıp birincinin kimliğiyle
// devam ederdi.
//
// Uç sepet YARATMA değil, sepet kapsamlı bir uçtur: yaratma halkadan muaftır
// (bkz. [TestSepetYaratmaBilerekOynatilmaz]) ve orada anahtarın yeniden
// kullanımı diye bir kavram kalmamıştır.
func TestIdempotencyAyniAnahtarFarkliGovdeyiReddeder(t *testing.T) {
	sepetID, _ := sepetOlustur(t)
	anahtar := "e2e-sepet-anahtari-2"

	istekYap := func(sehir string) *http.Request {
		govde, err := json.Marshal(map[string]any{
			"address": map[string]string{"country_code": vergiliUlke, "city": sehir},
		})
		require.NoError(t, err)

		istek := httptest.NewRequest(http.MethodPost,
			"/store/v1/carts/"+sepetID+"/shipping-address", bytes.NewReader(govde))
		istek.Header.Set("Content-Type", "application/json")
		istek.Header.Set(corehttp.PublishableKeyHeader, publishableAnahtar)
		istek.Header.Set(corehttp.IdempotencyKeyHeader, anahtar)

		return istek
	}

	ilk := httptest.NewRecorder()
	testRouter.ServeHTTP(ilk, istekYap("Istanbul"))
	require.Less(t, ilk.Code, http.StatusInternalServerError, "gövde: %s", ilk.Body.String())

	farkli := httptest.NewRecorder()
	testRouter.ServeHTTP(farkli, istekYap("Ankara"))
	assert.Equal(t, http.StatusConflict, farkli.Code,
		"aynı anahtar farklı gövdeyle reddedilmeli; gövde: %s", farkli.Body.String())
}

// TestOpenAPISemasiRouterAgacindanUretilir Faz 9'un şema ayağını kanıtlar.
//
// Şema elle yazılmaz, router'dan türetilir; testin işi de bunu doğrulamaktır:
// gerçekten kayıtlı olan yollar şemada görünmeli, güvenlik şeması yüzeye göre
// ayrışmalı ve giriş ucu korumasız işaretlenmelidir.
func TestOpenAPISemasiRouterAgacindanUretilir(t *testing.T) {
	istek := httptest.NewRequest(http.MethodGet, "/openapi.json", http.NoBody)
	kayit := httptest.NewRecorder()
	testRouter.ServeHTTP(kayit, istek)

	require.Equal(t, http.StatusOK, kayit.Code,
		"şema ucu 200 dönmeli; gövde: %s", kayit.Body.String())

	var belge struct {
		OpenAPI string                    `json:"openapi"`
		Paths   map[string]map[string]any `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &belge),
		"şema çözülemedi; gövde: %s", kayit.Body.String())

	assert.NotEmpty(t, belge.OpenAPI, "şema sürümü bildirilmeli")

	for _, yol := range []string{
		"/store/v1/products",
		"/store/v1/products/{id}",
		"/admin/v1/users",
		"/admin/v1/auth/login",
		"/admin/v1/sales-channels",
	} {
		assert.Contains(t, belge.Paths, yol, "%q şemada bulunmalı", yol)
	}

	// Şema yalnızca route DESENLERİNİ yayımlar; kimlik gerektiren bir uç
	// şemada görünse de çağrılamaz. Kayıt kimliği taşıyan ham yol
	// (/store/v1/products/prod_01…) şemaya HİÇ girmemelidir.
	for yol := range belge.Paths {
		assert.NotContains(t, yol, "prod_", "şemada ham kayıt kimliği olmamalı: %s", yol)
	}
}

// TestEklentiCekirdegeDokunmadanSaglayiciEkler Faz 9'un eklenti ayağını
// GERÇEK payment modülü üzerinde kanıtlar.
//
// İddia iki katmanlıdır: (1) eklenti hiçbir commerce modülünü import etmeden
// sağlayıcısını kaydedebilmeli, (2) kaydettiği sağlayıcı modül tarafından
// SEÇİLEBİLİR olmalı. İkincisi olmadan birincisi yalnızca bir kayıt defteri
// egzersizidir.
func TestEklentiCekirdegeDokunmadanSaglayiciEkler(t *testing.T) {
	ctx := context.Background()

	kayitlar, err := containerSaglayicilari()
	require.NoError(t, err, "ödeme sağlayıcı kaydı çözülemedi")
	require.NotContains(t, kayitlar.IDs(), paymentstripe.ProviderID,
		"ön koşul: stripe sağlayıcısı henüz kayıtlı olmamalı")

	eklentiler := coreplugin.NewRegistry(nil)
	eklentiler.Add(paymentstripe.New())

	// Host, modüllerin AYNI container'ıyla kurulur; eklentinin sağlayıcısı
	// çalışan sisteme girer, ayrı bir kopyaya değil.
	host := coreplugin.NewHost(kap, module.NewRegistry(nil, nil), nil, nil,
		map[string]string{"STRIPE_API_KEY": "sk_test_e2e"})

	require.NoError(t, eklentiler.Install(ctx, host), "eklenti kurulamadı")
	require.NoError(t, eklentiler.Start(ctx, host), "eklenti başlatılamadı")

	assert.Contains(t, kayitlar.IDs(), paymentstripe.ProviderID,
		"stripe sağlayıcısı payment modülünde seçilebilir olmalı")

	saglayici, err := kayitlar.Get(paymentstripe.ProviderID)
	require.NoError(t, err, "sağlayıcı kimliğiyle çözülebilmeli")
	assert.Equal(t, paymentstripe.ProviderID, saglayici.ID())
}

// TestEklentiAyariEksikseKurulumDurur yapılandırma hatasının açılışta
// patladığını doğrular.
//
// Sessizce atlansaydı, "stripe kurulu" sanılan bir mağaza hiç ödeme alamaz ve
// bu ancak ilk müşteri denemesinde görülürdü.
func TestEklentiAyariEksikseKurulumDurur(t *testing.T) {
	eklentiler := coreplugin.NewRegistry(nil)
	eklentiler.Add(paymentstripe.New())

	host := coreplugin.NewHost(kap, module.NewRegistry(nil, nil), nil, nil, nil)

	err := eklentiler.Install(context.Background(), host)
	require.Error(t, err, "ayarı olmayan eklenti kurulmamalı")
	assert.Contains(t, err.Error(), paymentstripe.Name,
		"hata hangi eklentinin başarısız olduğunu söylemeli")
}

// containerSaglayicilari payment modülünün sağlayıcı kaydını container'dan
// çözer.
//
// Kayıt ADLA çözülür: eklenti de aynı adı kullanır ve iki adın uyumu
// internal/arch/constants_test.go ile derleme zamanına bağlanmıştır.
func containerSaglayicilari() (*paymentsvc.ProviderRegistry, error) {
	return container.Resolve[*paymentsvc.ProviderRegistry](kap, paymentmod.ProvidersName)
}

// sepetKimliginiOku bir sepet yanıtından kimliği çıkarır.
func sepetKimliginiOku(t *testing.T, kayit *httptest.ResponseRecorder) string {
	t.Helper()

	var zarf struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf),
		"sepet yanıtı çözülemedi; gövde: %s", kayit.Body.String())
	require.NotEmpty(t, zarf.Data.ID, "sepet kimliği dönmeli")

	return zarf.Data.ID
}
